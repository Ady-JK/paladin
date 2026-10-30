// Package transcript owns the on-disk session store:
// <root>/<encode_cwd>/<uuid>/ with meta.json + messages.jsonl.
package transcript

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Ady-JK/paladin/core/internal/jsonx"
	"github.com/Ady-JK/paladin/core/internal/syserr"
	"github.com/Ady-JK/paladin/core/internal/toolset"
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Signature string `json:"signature,omitempty"`
}

type Message struct {
	Role       Role        `json:"role"`
	Content    string      `json:"content"`
	CreatedAt  time.Time   `json:"createdAt"`
	ToolCalls  *[]ToolCall `json:"toolCalls,omitempty"`
	ToolCallID *string     `json:"toolCallId,omitempty"`
	Name       *string     `json:"name,omitempty"`
	IsError    bool        `json:"isError,omitempty"`
	IsNotify   bool        `json:"isNotify,omitempty"`
	// Compaction marks a summary checkpoint: the model view restarts
	// here, with Content as the summary of everything before it.
	Compaction *Compaction `json:"compaction,omitempty"`
	// Replay marks a copy of a kept message re-recorded after a
	// compaction marker. It feeds the model view only; the original
	// already shows in history.
	Replay bool `json:"replay,omitempty"`
	// Display is the user's short form of an expanded user message
	// (`/review`, `/skill x …`); history and titles show it while the
	// model keeps seeing Content.
	Display string `json:"display,omitempty"`
	// Reasoning is an assistant message's visible thinking. It is shown
	// in history but never sent back to the model.
	Reasoning string `json:"reasoning,omitempty"`
}

// Compaction describes one context compaction checkpoint.
type Compaction struct {
	// TokensBefore is the estimated context size that triggered it.
	TokensBefore int64 `json:"tokensBefore"`
	// Reason is "auto" (threshold) or "overflow" (provider rejected).
	Reason string `json:"reason"`
}

// IsCompaction reports whether m is a compaction checkpoint marker.
func (m Message) IsCompaction() bool { return m.Role == RoleSystem && m.Compaction != nil }

type Meta struct {
	SchemaVersion uint32    `json:"schemaVersion"`
	SessionID     string    `json:"sessionId"`
	Cwd           string    `json:"cwd"`
	CreatedAt     time.Time `json:"createdAt"`
	Provider      *string   `json:"provider,omitempty"`
	Model         *string   `json:"model,omitempty"`
	// Title is a user-chosen name; empty means derive from messages.
	Title string `json:"title,omitempty"`
	// ForkedFrom is the source session id of a fork.
	ForkedFrom string `json:"forkedFrom,omitempty"`
	// Revert is a pending undo: messages from Index on are hidden and
	// dropped by the next turn, or brought back by a redo.
	Revert *Revert `json:"revert,omitempty"`
	// Worktree is set when the session works in a managed git worktree.
	Worktree *Worktree `json:"worktree,omitempty"`
	// Tools holds the session's tool switches and the tools it has seen.
	Tools *toolset.State `json:"tools,omitempty"`
	// BudgetUsd is the session's spend limit: nil inherits the app
	// default, 0 turns the limit off.
	BudgetUsd *float64 `json:"budgetUsd,omitempty"`
	// Subagent is set on a session an agent spawned: its parent link
	// and last known status.
	Subagent *Subagent `json:"subagent,omitempty"`
	// Children are the subagent sessions this session spawned, oldest
	// first.
	Children []string `json:"children,omitempty"`
}

// Subagent ties a child session to the parent that spawned it. A child
// lives in its own session folder beside the parent's; this link (and
// the parent's Children list) is what rebuilds the tree after a restart.
type Subagent struct {
	ParentID     string `json:"parentId"`
	ParentTurnID string `json:"parentTurnId,omitempty"`
	SpawnCallID  string `json:"spawnCallId,omitempty"`
	// Type is the subagent template ("explore", "reviewer").
	Type string `json:"type"`
	Name string `json:"name"`
	// Task is the assignment the child was spawned with.
	Task string `json:"task,omitempty"`
	// ForkTurns is "none" (task only) or "all" (parent history copied).
	ForkTurns string `json:"forkTurns,omitempty"`
	// Status is queued, running, completed, failed or interrupted.
	Status string `json:"status"`
	// Result is the last turn's final reply; Error its failure.
	Result string `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
	// Reported is set once the parent received the latest result.
	Reported bool `json:"reported,omitempty"`
	// CompletionID names the latest result (<child id>#<child turn id>).
	// The parent's transcript holds it once that result is saved there,
	// which is how a restart tells a delivered result from a lost one.
	CompletionID string    `json:"completionId,omitempty"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// Worktree ties a session to a managed git worktree.
type Worktree struct {
	RepoRoot   string `json:"repoRoot"`
	Path       string `json:"path"`
	Branch     string `json:"branch"`
	Base       string `json:"base"`
	BaseBranch string `json:"baseBranch,omitempty"`
	// Origin is the folder the worktree was made from; project-scoped
	// state such as folder memory stays keyed on it.
	Origin string `json:"origin"`
}

// ProjectCwd is the folder project-scoped state belongs to: the
// worktree's origin folder, or the session's own folder.
func (m Meta) ProjectCwd() string {
	if m.Worktree != nil && m.Worktree.Origin != "" {
		return m.Worktree.Origin
	}
	return m.Cwd
}

// Revert records a pending undo so it can be redone or committed.
type Revert struct {
	// Index is the transcript position of the reverted user message.
	Index int `json:"index"`
	// Pre is the workspace snapshot taken just before the revert.
	Pre string `json:"pre,omitempty"`
	// Restored lists the files the revert rewrote, each with the
	// snapshot entry it wrote ("" when it deleted the file).
	Restored []RevertFile `json:"restored,omitempty"`
	// Skipped lists files left alone because they changed since.
	Skipped []string `json:"skipped,omitempty"`
	// Snapshots is false when only the conversation was reverted.
	Snapshots bool `json:"snapshots"`
}

// RevertFile is one file rewritten by a revert.
type RevertFile struct {
	Path  string `json:"path"`
	Wrote string `json:"wrote"`
}

// MaxTitleRunes caps a user-chosen session title.
const MaxTitleRunes = 120

const SchemaVersion = 1

func UserMessage(content string) Message {
	return Message{Role: RoleUser, Content: content, CreatedAt: time.Now().UTC()}
}

// Visible in history: users always, assistants only when non-blank.
func (m Message) Visible() bool {
	if m.Replay {
		return false
	}
	switch m.Role {
	case RoleUser:
		return true
	case RoleAssistant:
		if strings.TrimSpace(m.Content) != "" {
			return true
		}
		return m.ToolCalls != nil && len(*m.ToolCalls) > 0
	default:
		return true
	}
}

// TitleFor returns the first user message truncated to 80 chars,
// used as session title.
func TitleFor(messages []Message) string {
	for _, msg := range messages {
		if msg.Role == RoleUser && msg.Visible() {
			title := strings.TrimSpace(msg.Display)
			if title == "" {
				title = strings.TrimSpace(msg.Content)
			}
			if title == "" {
				continue
			}
			return shorten(title, 80)
		}
	}
	return ""
}

func shorten(text string, n int) string {
	runes := []rune(text)
	if len(runes) <= n {
		return text
	}
	cut := string(runes[:n])
	if i := strings.LastIndexAny(cut, " \n\t"); i > n/2 {
		return strings.TrimRight(cut[:i], " \n\t\r") + "…"
	}
	return strings.TrimRight(cut, " \n\t\r") + "…"
}

// Session is a session: metadata plus its on-disk directory.
type Session struct {
	Meta Meta
	Dir  string
}

func (s *Session) Append(role Role, content string) error {
	return s.AppendMessage(Message{
		Role:      role,
		Content:   content,
		CreatedAt: time.Now().UTC(),
	})
}

func (s *Session) AppendMessage(msg Message) error {
	return s.appendMessages([]Message{msg})
}

// turnLine is how a whole turn is stored: one JSONL line, so a crash or
// a failed write leaves all of the turn or none of it. Older
// transcripts hold one message per line; readers accept both.
type turnLine struct {
	Turn turnBody `json:"turn"`
}

type turnBody struct {
	Messages []Message `json:"messages"`
}

// AppendTurn appends a turn's messages as one line.
func (s *Session) AppendTurn(msgs []Message) error {
	if len(msgs) == 0 {
		return nil
	}
	stamped := make([]Message, len(msgs))
	for i, m := range msgs {
		stamped[i] = stampTime(m)
	}
	line, err := jsonx.Marshal(turnLine{Turn: turnBody{Messages: stamped}})
	if err != nil {
		return fmt.Errorf("io: encode turn: %s", syserr.Str(err))
	}
	return s.appendLines(append(line, '\n'))
}

// appendMessages appends msgs one per line in a single write.
func (s *Session) appendMessages(msgs []Message) error {
	var buf []byte
	for _, m := range msgs {
		line, err := jsonx.Marshal(stampTime(m))
		if err != nil {
			return fmt.Errorf("io: encode message: %s", syserr.Str(err))
		}
		buf = append(append(buf, line...), '\n')
	}
	if len(buf) == 0 {
		return nil
	}
	return s.appendLines(buf)
}

func stampTime(m Message) Message {
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now().UTC()
	}
	return m
}

// syncFile is replaced in tests to simulate a failing disk.
var syncFile = (*os.File).Sync

// appendLines writes complete lines and syncs them. On failure the file
// is cut back to its previous size, so an error never leaves a partial
// write behind for a retry to duplicate.
func (s *Session) appendLines(data []byte) error {
	f, err := os.OpenFile(filepath.Join(s.Dir, "messages.jsonl"), os.O_CREATE|os.O_APPEND|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("io: open transcript: %s", syserr.Str(err))
	}
	defer f.Close()
	if err := dropTornTail(f); err != nil {
		return fmt.Errorf("io: repair transcript: %s", syserr.Str(err))
	}
	st, err := f.Stat()
	if err != nil {
		return fmt.Errorf("io: stat transcript: %s", syserr.Str(err))
	}
	rollback := func(cause error, what string) error {
		if terr := f.Truncate(st.Size()); terr != nil {
			return fmt.Errorf("io: %s: %s (rollback failed: %s)", what, syserr.Str(cause), syserr.Str(terr))
		}
		return fmt.Errorf("io: %s: %s", what, syserr.Str(cause))
	}
	if _, err := f.Write(data); err != nil {
		return rollback(err, "write transcript")
	}
	// Sync so a crash cannot silently drop the tail of the transcript;
	// turn volume is low enough that the cost is noise.
	if err := syncFile(f); err != nil {
		return rollback(err, "sync transcript")
	}
	return nil
}

// SaveMeta writes the session's metadata atomically.
func (s *Session) SaveMeta() error {
	return writeMeta(s.Dir, s.Meta)
}

// Truncate keeps the first n transcript messages, atomically.
func (s *Session) Truncate(n int) error {
	msgs, err := s.History()
	if err != nil {
		return err
	}
	if n < 0 || n >= len(msgs) {
		return nil
	}
	// The list summary covers the old bytes; drop it on both sides of
	// the rewrite so a list in between cannot cache the old file.
	if err := dropSummary(s.Dir); err != nil {
		return err
	}
	defer func() { _ = dropSummary(s.Dir) }()
	var buf []byte
	for _, m := range msgs[:n] {
		line, err := jsonx.Marshal(m)
		if err != nil {
			return fmt.Errorf("io: encode message: %s", syserr.Str(err))
		}
		buf = append(append(buf, line...), '\n')
	}
	return atomicWrite(s.Dir, "messages.jsonl", buf)
}

func (s *Session) History() ([]Message, error) {
	return LoadTranscript(s.Dir)
}

// UsagePath is the per-session token/spend ledger. It lives next to
// meta.json so per-session accounting survives restarts and needs no
// migration of the transcript itself.
func (s *Session) UsagePath() string {
	return filepath.Join(s.Dir, "usage.json")
}
