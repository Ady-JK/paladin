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
	if msg.CreatedAt.IsZero() {
		msg.CreatedAt = time.Now().UTC()
	}
	f, err := os.OpenFile(filepath.Join(s.Dir, "messages.jsonl"), os.O_CREATE|os.O_APPEND|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("io: open transcript: %s", syserr.Str(err))
	}
	defer f.Close()
	if err := dropTornTail(f); err != nil {
		return fmt.Errorf("io: repair transcript: %s", syserr.Str(err))
	}
	line, err := jsonx.Marshal(msg)
	if err != nil {
		return fmt.Errorf("io: encode message: %s", syserr.Str(err))
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("io: write message: %s", syserr.Str(err))
	}
	// Sync each append so a crash cannot silently drop the tail of the
	// transcript; message volume is low enough that the cost is noise.
	if err := f.Sync(); err != nil {
		return fmt.Errorf("io: sync message: %s", syserr.Str(err))
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
