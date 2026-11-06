// Package session owns client-facing history shapes: session infos,
// history rows, and background-job reminder helpers.
package session

import (
	"encoding/json"
	"strings"

	"github.com/Ady-JK/paladin/core/internal/media"
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
	// Subagent links a child session to the parent that spawned it.
	Subagent *SubagentInfo `json:"subagent,omitempty"`
	// Children lists the subagent sessions this session spawned.
	Children []string `json:"children,omitempty"`
	// Damaged says why the session could not be read (it can still be
	// deleted).
	Damaged string `json:"damaged,omitempty"`
}

// SubagentInfo is the client view of a child session's link and state.
type SubagentInfo struct {
	ParentID string `json:"parentId"`
	// SpawnCallID is the parent's spawn_agent call that created it.
	SpawnCallID string `json:"spawnCallId,omitempty"`
	Type        string `json:"type"`
	Name        string `json:"name"`
	Task        string `json:"task,omitempty"`
	ForkTurns   string `json:"forkTurns,omitempty"`
	// Status is queued, running, completed, failed or interrupted.
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`
	UpdatedAt int64  `json:"updatedAt"`
}

// SubagentView converts a stored subagent link for the client.
func SubagentView(sa *transcript.Subagent) *SubagentInfo {
	if sa == nil {
		return nil
	}
	return &SubagentInfo{
		ParentID: sa.ParentID, SpawnCallID: sa.SpawnCallID, Type: sa.Type, Name: sa.Name, Task: sa.Task, ForkTurns: sa.ForkTurns,
		Status: sa.Status, Error: sa.Error, UpdatedAt: sa.UpdatedAt.UnixMilli(),
	}
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
		Subagent:     SubagentView(meta.Subagent),
		Children:     meta.Children,
	}
}

// HistoryMessage is the client-facing history row: tool rows link back
// to their call; reminders keep their job link instead.
type HistoryMessage struct {
	Arguments   *string            `json:"arguments,omitempty"`
	Image       *media.Image       `json:"image,omitempty"`
	Attachments []media.Attachment `json:"attachments,omitempty"`
	Content     string             `json:"content"`
	CreatedAt   int64              `json:"createdAt"`
	// Index is a user row's transcript position, the handle for undo.
	Index *int `json:"index,omitempty"`
	// Kind tags special rows; "compaction" is a context-compaction
	// divider whose Content is the summary.
	Kind       *string `json:"kind,omitempty"`
	Name       *string `json:"name,omitempty"`
	Role       string  `json:"role"`
	Status     *string `json:"status,omitempty"`
	ToolCallID *string `json:"toolCallId,omitempty"`
	// Stats is a user row's turn cost, so a reopened session can show
	// the same summary the live turn did.
	Stats *transcript.TurnStats `json:"stats,omitempty"`
}

// HistoryView renders messages as history rows.
func HistoryView(msgs []transcript.Message) []HistoryMessage {
	return HistoryViewStats(msgs, nil)
}

// HistoryViewStats is HistoryView with per-turn costs: statsAt maps the
// transcript index of a turn's last message to that turn's cost, and the
// row built from that message carries the summary.
//
// The end of a turn is the reliable anchor. Its start is not: a resumed
// turn (a steer, or a parent driving a subagent) begins with an
// assistant or tool message rather than a user row, and the newest page
// of a long session begins mid-turn, so the turn's first message need
// not be in msgs at all. Every page that holds any part of a turn holds
// its end, so the summary survives paging and a restart.
func HistoryViewStats(msgs []transcript.Message, statsAt map[int]*transcript.TurnStats) []HistoryMessage {
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
		// Stats land on the row built from the turn's last message; see
		// the function comment. Deleted once claimed so a caller reusing
		// the map does not attach the same cost twice.
		var turnStats *transcript.TurnStats
		if stats, ok := statsAt[i]; ok {
			turnStats = stats
			delete(statsAt, i)
		}
		if msg.IsCompaction() {
			kind := "compaction"
			out = append(out, HistoryMessage{
				Role: "system", Content: msg.Content,
				CreatedAt: msg.CreatedAt.Unix(), Kind: &kind, Stats: turnStats,
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
				Image:      msg.Image,
				Content:    msg.Content,
				CreatedAt:  msg.CreatedAt.Unix(),
				ToolCallID: msg.ToolCallID,
				Name:       msg.Name,
				Arguments:  argsPtr,
				Status:     &status,
				Stats:      turnStats,
			})
			continue
		}
		if msg.Role == transcript.RoleAssistant && strings.TrimSpace(msg.Reasoning) != "" {
			kind := "thinking"
			out = append(out, HistoryMessage{
				Role: "assistant", Content: msg.Reasoning,
				CreatedAt: msg.CreatedAt.Unix(), Kind: &kind, Stats: turnStats,
			})
			// The thinking row took the summary; the reply starts a
			// second row for the same message.
			turnStats = nil
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
			Role:        role,
			Content:     content,
			CreatedAt:   msg.CreatedAt.Unix(),
			Attachments: msg.Attachments,
			Stats:       turnStats,
		}
		if msg.Role == transcript.RoleUser && !IsNotifyMessage(msg) {
			idx := i
			row.Index = &idx
		}
		out = append(out, row)
	}
	// A page can start in the middle of a turn, so the turn's first
	// message may not be in msgs at all.
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

// HistoryPage is one window of the transcript. Start is the row
// ordinal of Messages[0]; rows are numbered from the start of the
// session, so appending a turn never shifts an older page.
type HistoryPage struct {
	Messages []HistoryMessage `json:"messages"`
	Start    int              `json:"start"`
	HasMore  bool             `json:"hasMore"`
	Total    int              `json:"total"`
}

// PageHistory slices rows into the window ending just before before
// (0 means the newest rows) and at most limit long.
func PageHistory(rows []HistoryMessage, limit, before int) HistoryPage {
	if limit <= 0 {
		limit = 120
	}
	total := len(rows)
	if before <= 0 || before > total {
		before = total
	}
	start := before - limit
	if start < 0 {
		start = 0
	}
	if rows == nil {
		rows = []HistoryMessage{}
	}
	return HistoryPage{
		Messages: rows[start:before],
		Start:    start,
		HasMore:  start > 0,
		Total:    total,
	}
}

// ImageRef points at one image a transcript row carries. Index is the
// row's position in the visible transcript (the same numbering user
// rows use for undo).
type ImageRef struct {
	Index int    `json:"index"`
	Role  string `json:"role"`
	Path  string `json:"path"`
	Name  string `json:"name,omitempty"`
}

// ImagesView is what the renderer needs to mount and register images
// without downloading the whole transcript after every turn.
type ImagesView struct {
	LatestUser int        `json:"latestUser"`
	Images     []ImageRef `json:"images"`
}

// ImagesViewOf collects image references from a visible transcript.
// LatestUser is the index of the last user row (-1 when there is none),
// so a finished turn can mount just that row's attachments.
func ImagesViewOf(msgs []transcript.Message) ImagesView {
	out := ImagesView{LatestUser: -1, Images: []ImageRef{}}
	for i, m := range msgs {
		if m.Replay || !m.Visible() {
			continue
		}
		if m.Role == transcript.RoleUser {
			out.LatestUser = i
		}
		if m.Image != nil {
			out.Images = append(out.Images, ImageRef{Index: i, Role: string(m.Role), Path: m.Image.Path})
		}
		for _, a := range m.Attachments {
			if a.Image != nil {
				out.Images = append(out.Images, ImageRef{Index: i, Role: string(m.Role), Path: a.Image.Path, Name: a.Name})
			}
		}
	}
	return out
}
