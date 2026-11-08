// Journal keeps a crash-safe checkpoint of an active turn separately from
// the transcript, whose completed turns remain one line each.
package transcript

import (
	"bufio"
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

type journalState struct {
	ID        string    `json:"id"`
	StartSize int64     `json:"startSize"`
	Messages  []Message `json:"messages"`
	Partial   string    `json:"partial,omitempty"`
}

type TurnJournal struct {
	ID     string
	dir    string
	state  journalState
	unlock func()
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
	j := &TurnJournal{ID: id, dir: s.Dir, unlock: unlock, state: journalState{ID: id, StartSize: size}}
	if err := j.Checkpoint(messages, ""); err != nil {
		j.Release()
		return nil, err
	}
	return j, nil
}

func (j *TurnJournal) Checkpoint(messages []Message, partial string) error {
	j.state.Messages = messages
	j.state.Partial = partial
	data, err := json.Marshal(j.state)
	if err != nil {
		return err
	}
	return atomicWrite(j.dir, journalFile, data)
}

// Finish removes the journal only after the transcript was committed.
func (j *TurnJournal) Finish() error {
	if err := os.Remove(filepath.Join(j.dir, journalFile)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (j *TurnJournal) Release() {
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
	if err := os.Remove(filepath.Join(s.Dir, journalFile)); err != nil && !os.IsNotExist(err) {
		return false, err
	}
	return !committed, nil
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
