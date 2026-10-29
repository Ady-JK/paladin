// Package session owns client-facing history shapes: session infos,
// history rows, and background-job reminder helpers.
package session

import (
	"encoding/json"
	"strings"

	"github.com/Ady-JK/paladin/core/internal/transcript"
)

// TodoEntry is one client-facing activity row from the session's
// todo list.
type TodoEntry struct {
	Content string `json:"content"`
	Status  string `json:"status"`
}

const (
	ToolRunning = "running"
	ToolOK      = "ok"
	ToolFailed  = "failed"
)

// NotifySuffix marks reminder rows; older rows use the id suffix only.
const NotifySuffix = "#notify"

func NotifyCallID(callID string) string {
	return callID + NotifySuffix
}

func NotifyArgs(jobID string) string {
	out, _ := json.Marshal(map[string]string{"action": "notify", "job_id": jobID})
	return string(out)
}

func IsNotifyMessage(msg transcript.Message) bool {
	if msg.IsNotify {
		return true
	}
	return msg.ToolCallID != nil && strings.HasSuffix(*msg.ToolCallID, NotifySuffix)
}

// SessionInfo is the client-facing session row (camelCase, matches the
// desktop frontend and PROTOCOL.md).
type SessionInfo struct {
	CreatedAt    int64  `json:"createdAt"`
	Cwd          string `json:"cwd"`
	MessageCount int    `json:"messageCount"`
	Model        string `json:"model"`
	Provider     string `json:"provider"`
	SessionID    string `json:"sessionId"`
	Title        string `json:"title"`
	// Revert is the session's pending undo, if any.
	Revert *RevertInfo `json:"revert,omitempty"`
	// Worktree is set for sessions working in a managed git worktree.
	Worktree *WorktreeInfo `json:"worktree,omitempty"`
}

// WorktreeInfo is the client view of a session's worktree.
type WorktreeInfo struct {
	RepoRoot   string `json:"repoRoot"`
	Path       string `json:"path"`
	Branch     string `json:"branch"`
	BaseBranch string `json:"baseBranch,omitempty"`
	Origin     string `json:"origin"`
}

// WorktreeView converts a stored worktree link for the client.
func WorktreeView(wt *transcript.Worktree) *WorktreeInfo {
	if wt == nil {
		return nil
	}
	return &WorktreeInfo{RepoRoot: wt.RepoRoot, Path: wt.Path, Branch: wt.Branch, BaseBranch: wt.BaseBranch, Origin: wt.Origin}
}

// RevertInfo is the client view of a pending undo.
type RevertInfo struct {
	Index     int      `json:"index"`
	Restored  []string `json:"restored"`
	Skipped   []string `json:"skipped"`
	Snapshots bool     `json:"snapshots"`
}

// RevertView converts a stored revert for the client.
func RevertView(rv *transcript.Revert) *RevertInfo {
	if rv == nil {
		return nil
	}
	out := &RevertInfo{Index: rv.Index, Restored: []string{}, Skipped: append([]string{}, rv.Skipped...), Snapshots: rv.Snapshots}
	for _, f := range rv.Restored {
		out.Restored = append(out.Restored, f.Path)
	}
	return out
}

func InfoFrom(meta transcript.Meta, messageCount int, title string) SessionInfo {
	provider := ""
	if meta.Provider != nil {
		provider = *meta.Provider
	}
	model := ""
	if meta.Model != nil {
		model = *meta.Model
	}
	return SessionInfo{
		SessionID:    meta.SessionID,
		Cwd:          meta.Cwd,
		CreatedAt:    meta.CreatedAt.Unix(),
		Provider:     provider,
		Model:        model,
		MessageCount: messageCount,
		Title:        title,
		Revert:       RevertView(meta.Revert),
		Worktree:     WorktreeView(meta.Worktree),
	}
}

// HistoryMessage is the client-facing history row: tool rows link back
// to their call; reminders keep their job link instead.
type HistoryMessage struct {
	Arguments *string `json:"arguments,omitempty"`
	Content   string  `json:"content"`
	CreatedAt int64   `json:"createdAt"`
	// Index is a user row's transcript position, the handle for undo.
	Index *int `json:"index,omitempty"`
	// Kind tags special rows; "compaction" is a context-compaction
	// divider whose Content is the summary.
	Kind       *string `json:"kind,omitempty"`
	Name       *string `json:"name,omitempty"`
	Role       string  `json:"role"`
	Status     *string `json:"status,omitempty"`
	ToolCallID *string `json:"toolCallId,omitempty"`
}

func HistoryView(msgs []transcript.Message) []HistoryMessage {
	calls := map[string]transcript.ToolCall{}
	for _, msg := range msgs {
		if msg.Role != transcript.RoleAssistant {
			continue
		}
		if msg.ToolCalls == nil {
			continue
		}
		for _, call := range *msg.ToolCalls {
			if call.ID == "" {
				continue
			}
			if _, ok := calls[call.ID]; !ok {
				calls[call.ID] = call
			}
		}
	}
	var out []HistoryMessage
	for i, msg := range msgs {
		if msg.Replay {
			continue
		}
		if msg.IsCompaction() {
			kind := "compaction"
			out = append(out, HistoryMessage{
				Role: "system", Content: msg.Content,
				CreatedAt: msg.CreatedAt.Unix(), Kind: &kind,
			})
			continue
		}
		if msg.Role == transcript.RoleTool {
			args := ""
			if msg.ToolCallID != nil {
				if c, ok := calls[*msg.ToolCallID]; ok {
					args = c.Arguments
				}
			}
			if args == "" && IsNotifyMessage(msg) {
				args = notifyHistoryArgs(msg.Content)
			}
			var argsPtr *string
			if args != "" {
				argsPtr = &args
			}
			status := ToolOK
			if msg.IsError {
				status = ToolFailed
			}
			out = append(out, HistoryMessage{
				Role:       "tool",
				Content:    msg.Content,
				CreatedAt:  msg.CreatedAt.Unix(),
				ToolCallID: msg.ToolCallID,
				Name:       msg.Name,
				Arguments:  argsPtr,
				Status:     &status,
			})
			continue
		}
		if !msg.Visible() {
			continue
		}
		var role string
		switch msg.Role {
		case transcript.RoleUser:
			role = "user"
		case transcript.RoleAssistant:
			role = "assistant"
		case transcript.RoleSystem:
			role = "system"
		default:
			role = "tool"
		}
		content := msg.Content
		if msg.Role == transcript.RoleUser && msg.Display != "" {
			content = msg.Display
		}
		row := HistoryMessage{
			Role:      role,
			Content:   content,
			CreatedAt: msg.CreatedAt.Unix(),
		}
		if msg.Role == transcript.RoleUser && !IsNotifyMessage(msg) {
			idx := i
			row.Index = &idx
		}
		out = append(out, row)
	}
	if out == nil {
		out = []HistoryMessage{}
	}
	return out
}

// TodoFileName is the on-disk activity list; kept here so the runtime
// can serve it without importing the tools package.
const TodoFileName = "todos.json"

// TodosView parses a stored todos.json payload into client rows. A
// missing or empty payload is an empty list.
func TodosView(data []byte) []TodoEntry {
	var stored struct {
		Todos []TodoEntry `json:"todos"`
	}
	if len(data) == 0 {
		return []TodoEntry{}
	}
	if err := json.Unmarshal(data, &stored); err != nil {
		return []TodoEntry{}
	}
	if stored.Todos == nil {
		return []TodoEntry{}
	}
	return stored.Todos
}

// TodosFinished reports whether a stored activity list holds only
// terminal work: every item finished or cancelled. A missing,
// unparsable, or empty list is not finished — only a real plan whose
// every step landed counts, so a corrupt file is never mistaken for
// done work and silently dropped.
func TodosFinished(data []byte) bool {
	var stored struct {
		Todos []TodoEntry `json:"todos"`
	}
	if err := json.Unmarshal(data, &stored); err != nil {
		return false
	}
	if len(stored.Todos) == 0 {
		return false
	}
	for _, td := range stored.Todos {
		if td.Status != "finished" && td.Status != "cancelled" {
			return false
		}
	}
	return true
}

func notifyHistoryArgs(content string) string {
	var wire struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal([]byte(content), &wire); err != nil || wire.JobID == "" {
		return ""
	}
	return NotifyArgs(wire.JobID)
}
