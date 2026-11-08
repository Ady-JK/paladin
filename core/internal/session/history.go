// history.go builds client-facing transcript rows and pages.

package session

import (
	"strings"

	"github.com/Ady-JK/paladin/core/internal/media"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

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
	Stats      *transcript.TurnStats `json:"stats,omitempty"`
	Protection *SnapshotProtection   `json:"protection,omitempty"`
}

// SnapshotProtection describes the undo coverage for one user turn.
type SnapshotProtection struct {
	Turn      int      `json:"turn"`
	Floor     int      `json:"floor,omitempty"`
	State     string   `json:"state"`
	Reason    string   `json:"reason,omitempty"`
	Excluded  []string `json:"excluded,omitempty"`
	Truncated bool     `json:"truncated,omitempty"`
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
	return historyViewRange(msgs, statsAt, 0, -1)
}

// historyRowCount counts the visible rows a stored message contributes.
// An assistant message may produce both thinking and answer rows.
func historyRowCount(msg transcript.Message) int {
	if msg.Replay {
		return 0
	}
	if msg.IsCompaction() || msg.Role == transcript.RoleTool {
		return 1
	}
	count := 0
	if msg.Role == transcript.RoleAssistant && strings.TrimSpace(msg.Reasoning) != "" {
		count++
	}
	if msg.Visible() {
		count++
	}
	return count
}

// historyViewRange constructs only rows in [start,end); a negative end
// means all remaining rows. Tool calls are indexed from the entire history
// so a result at a page boundary keeps its arguments.
func historyViewRange(msgs []transcript.Message, statsAt map[int]*transcript.TurnStats, start, end int) []HistoryMessage {
	// Only tool results on the requested page need their call arguments.
	// Full-history rendering keeps the original complete index.
	var wanted map[string]bool
	if end >= 0 {
		wanted = map[string]bool{}
		ordinal := 0
		for _, msg := range msgs {
			count := historyRowCount(msg)
			if ordinal >= end {
				break
			}
			if ordinal >= start && count > 0 && msg.Role == transcript.RoleTool && msg.ToolCallID != nil && *msg.ToolCallID != "" {
				wanted[*msg.ToolCallID] = true
			}
			ordinal += count
		}
	}
	calls := map[string]transcript.ToolCall{}
	for _, msg := range msgs {
		if wanted != nil && len(wanted) == 0 {
			break
		}
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
			if wanted != nil && !wanted[call.ID] {
				continue
			}
			if _, ok := calls[call.ID]; !ok {
				calls[call.ID] = call
				if wanted != nil {
					delete(wanted, call.ID)
				}
			}
		}
	}
	var out []HistoryMessage
	ordinal := 0
	add := func(row HistoryMessage) {
		if ordinal >= start && (end < 0 || ordinal < end) {
			out = append(out, row)
		}
		ordinal++
	}
	for i, msg := range msgs {
		count := historyRowCount(msg)
		if count == 0 {
			continue
		}
		if ordinal+count <= start {
			ordinal += count
			continue
		}
		if end >= 0 && ordinal >= end {
			break
		}
		// Stats land on the row built from the turn's last message; see
		// the function comment. The lookup is by index, so nothing has
		// to be consumed from statsAt (and this stays safe for callers
		// that hand the same map to two views).
		turnStats := statsAt[i]
		if msg.IsCompaction() {
			kind := "compaction"
			add(HistoryMessage{
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
			add(HistoryMessage{
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
			add(HistoryMessage{
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
		add(row)
	}
	// A page can start in the middle of a turn, so the turn's first
	// message may not be in msgs at all.
	if out == nil {
		out = []HistoryMessage{}
	}
	return out
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

// HistoryPageStats renders only the requested window while preserving the
// full history's row numbering, tool-call links, and turn statistics.
func HistoryPageStats(msgs []transcript.Message, stats map[int]*transcript.TurnStats, limit, before int) HistoryPage {
	if limit <= 0 {
		limit = 120
	}
	total := 0
	for _, msg := range msgs {
		total += historyRowCount(msg)
	}
	if before <= 0 || before > total {
		before = total
	}
	start := max(0, before-limit)
	return HistoryPage{Messages: historyViewRange(msgs, stats, start, before), Start: start, HasMore: start > 0, Total: total}
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
