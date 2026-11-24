// Journal keeps a crash-safe checkpoint of an active turn separately from
// the transcript, whose completed turns remain one line each.
package transcript

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

const journalFile = "active-turn.json"

const interruptedText = "Turn interrupted because Paladin stopped unexpectedly. Review the recorded work and send a message to continue. Tools with unknown results may have run; inspect their effects before retrying."
const unknownToolResult = "The turn was interrupted before this tool's result was saved. It may have run or partially completed. Inspect its effects before deciding whether to retry; do not repeat it blindly."

// A journal is three files: active-turn.json (the header, written once, whose
// presence marks an interrupted turn), active-turn.msgs (one message per line,
// rewritten only from the first line that changed) and active-turn.partial
// (the reply being streamed). A checkpoint therefore costs what changed, not
// the length of the turn. Older versions kept everything in the header's
// "messages" and "partial"; recovery still reads that.
const (
	journalMsgs    = "active-turn.msgs"
	journalPartial = "active-turn.partial"
)

type journalState struct {
	ID        string    `json:"id"`
	StartSize int64     `json:"startSize"`
	Messages  []Message `json:"messages,omitempty"` // older single-file journals
	Partial   string    `json:"partial,omitempty"`  // older single-file journals
}

type TurnJournal struct {
	ID      string
	dir     string
	unlock  func()
	msgs    *os.File
	lines   [][]byte // the lines of active-turn.msgs, newline included
	partial string
}

// BeginJournal must succeed before a turn may run side-effecting tools.
func (s *Session) BeginJournal(messages []Message) (*TurnJournal, error) {
	unlock, acquired, err := tryJournalLock(filepath.Join(s.Dir, "active-turn.lock"))
	if err != nil {
		return nil, err
	}
	if !acquired {
		return nil, errors.New("another process is using this session")
	}
	if _, err := os.Stat(filepath.Join(s.Dir, journalFile)); err == nil {
		unlock()
		return nil, errors.New("an interrupted turn must be recovered before starting another")
	} else if !os.IsNotExist(err) {
		unlock()
		return nil, err
	}
	var size int64
	f, err := os.OpenFile(filepath.Join(s.Dir, "messages.jsonl"), os.O_RDWR, 0o600)
	if err == nil {
		if err = dropTornTail(f); err == nil {
			var st os.FileInfo
			st, err = f.Stat()
			if err == nil {
				size = st.Size()
			}
		}
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			unlock()
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		unlock()
		return nil, err
	}
	id := uuid.NewString()
	j := &TurnJournal{ID: id, dir: s.Dir, unlock: unlock}
	if err := j.begin(journalState{ID: id, StartSize: size}); err != nil {
		j.Release()
		return nil, err
	}
	if err := j.Checkpoint(messages, ""); err != nil {
		j.Release()
		return nil, err
	}
	return j, nil
}

// begin creates the message file (durably, with its directory entry) before
// the header that marks the journal as live.
func (j *TurnJournal) begin(state journalState) error {
	if err := atomicWrite(j.dir, journalMsgs, nil); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(j.dir, journalMsgs), os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	j.msgs = f
	header, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return atomicWrite(j.dir, journalFile, header)
}

// Checkpoint records the turn so far. Messages that did not change since the
// last checkpoint are not written again: the file is cut back to the first
// line that differs and rewritten from there, so a message that changed or
// moved is replaced, never duplicated.
func (j *TurnJournal) Checkpoint(messages []Message, partial string) error {
	lines := make([][]byte, len(messages))
	for i := range messages {
		line, err := json.Marshal(messages[i])
		if err != nil {
			return err
		}
		lines[i] = append(line, '\n')
	}
	same := 0
	for same < len(lines) && same < len(j.lines) && bytes.Equal(lines[same], j.lines[same]) {
		same++
	}
	if same < len(lines) || same < len(j.lines) {
		if err := j.rewriteFrom(same, lines); err != nil {
			return err
		}
	}
	if partial == j.partial {
		return nil
	}
	if partial == "" {
		if err := os.Remove(filepath.Join(j.dir, journalPartial)); err != nil && !os.IsNotExist(err) {
			return err
		}
	} else if err := atomicWrite(j.dir, journalPartial, []byte(partial)); err != nil {
		return err
	}
	j.partial = partial
	return nil
}

// rewriteFrom cuts the message file back to its first n lines and writes
// the rest of lines after them.
func (j *TurnJournal) rewriteFrom(n int, lines [][]byte) error {
	if j.msgs == nil {
		return errors.New("journal is closed")
	}
	var offset int64
	for _, l := range j.lines[:n] {
		offset += int64(len(l))
	}
	if err := j.msgs.Truncate(offset); err != nil {
		return err
	}
	if _, err := j.msgs.Seek(offset, io.SeekStart); err != nil {
		return err
	}
	if _, err := j.msgs.Write(bytes.Join(lines[n:], nil)); err != nil {
		return err
	}
	if err := j.msgs.Sync(); err != nil {
		return err
	}
	j.lines = lines
	return nil
}

// Finish removes the journal only after the transcript was committed.
func (j *TurnJournal) Finish() error {
	j.closeFiles()
	return removeJournal(j.dir)
}

func (j *TurnJournal) closeFiles() {
	if j.msgs != nil {
		_ = j.msgs.Close()
		j.msgs = nil
	}
}

// removeJournal deletes the header last, so a crash in between still leaves
// a journal that recovery finishes cleaning up.
func removeJournal(dir string) error {
	for _, name := range []string{journalMsgs, journalPartial, journalFile} {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func (j *TurnJournal) Release() {
	j.closeFiles()
	if j.unlock != nil {
		j.unlock()
		j.unlock = nil
	}
}

// RecoverInterrupted is safe to call on opening a session. A lock held by
// a live process leaves its journal alone. Recovery never executes tools.
func (s *Session) RecoverInterrupted() (bool, error) {
	if _, err := os.Stat(filepath.Join(s.Dir, journalFile)); os.IsNotExist(err) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	unlock, acquired, err := tryJournalLock(filepath.Join(s.Dir, "active-turn.lock"))
	if err != nil || !acquired {
		return false, err
	}
	defer unlock()
	data, err := os.ReadFile(filepath.Join(s.Dir, journalFile))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var state journalState
	if err := json.Unmarshal(data, &state); err != nil {
		return false, fmt.Errorf("read interrupted turn: %w", err)
	}
	if state.ID == "" || state.StartSize < 0 {
		return false, errors.New("invalid interrupted turn journal")
	}
	if err := s.readJournalBody(&state); err != nil {
		return false, err
	}
	committed, err := s.journalCommitted(state)
	if err != nil {
		return false, err
	}
	if !committed {
		messages := settleInterrupted(state.Messages)
		if strings.TrimSpace(state.Partial) != "" {
			messages = append(messages, Message{Role: RoleAssistant, Content: state.Partial, CreatedAt: time.Now().UTC()})
		}
		messages = append(messages, Message{Role: RoleAssistant, Content: interruptedText, CreatedAt: time.Now().UTC()})
		if err := s.AppendJournalTurn(messages, nil, state.ID); err != nil {
			return false, err
		}
	}
	if err := removeJournal(s.Dir); err != nil {
		return false, err
	}
	return !committed, nil
}

// readJournalBody adds the messages and the partial reply the newer journal
// keeps beside its header to state. A torn last line (the process died
// mid-append) is dropped.
func (s *Session) readJournalBody(state *journalState) error {
	data, err := os.ReadFile(filepath.Join(s.Dir, journalMsgs))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, raw := range bytes.SplitAfter(data, []byte("\n")) {
		if len(raw) == 0 || raw[len(raw)-1] != '\n' {
			continue
		}
		var m Message
		if err := json.Unmarshal(raw, &m); err != nil {
			return fmt.Errorf("read interrupted turn: %w", err)
		}
		state.Messages = append(state.Messages, m)
	}
	if partial, err := os.ReadFile(filepath.Join(s.Dir, journalPartial)); err == nil {
		state.Partial = string(partial)
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}

func settleInterrupted(messages []Message) []Message {
	results := make(map[string]bool)
	for _, m := range messages {
		if m.Role == RoleTool && m.ToolCallID != nil {
			results[*m.ToolCallID] = true
		}
	}
	out := make([]Message, 0, len(messages)+1)
	for _, m := range messages {
		out = append(out, m)
		if m.Role != RoleAssistant || m.ToolCalls == nil {
			continue
		}
		for _, call := range *m.ToolCalls {
			if results[call.ID] {
				continue
			}
			id, name := call.ID, call.Name
			out = append(out, Message{Role: RoleTool, Content: unknownToolResult, ToolCallID: &id, Name: &name, IsError: true, CreatedAt: time.Now().UTC()})
		}
	}
	return out
}

func (s *Session) journalCommitted(state journalState) (bool, error) {
	f, err := os.Open(filepath.Join(s.Dir, "messages.jsonl"))
	if os.IsNotExist(err) && state.StartSize == 0 {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return false, err
	}
	if st.Size() < state.StartSize {
		return false, errors.New("transcript changed while a turn was interrupted")
	}
	if _, err := f.Seek(state.StartSize, io.SeekStart); err != nil {
		return false, err
	}
	reader := bufio.NewReader(f)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 && line[len(line)-1] == '\n' {
			var turn turnLine
			if json.Unmarshal(line, &turn) == nil && turn.Turn.JournalID == state.ID {
				return true, nil
			}
			return false, errors.New("transcript changed while a turn was interrupted")
		}
		if err == io.EOF {
			return false, nil
		}
		if err != nil {
			return false, err
		}
	}
}
