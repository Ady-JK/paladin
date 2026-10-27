// Package session owns client-facing history shapes: session infos,
// history rows, and background-job reminder helpers.
package session

import (
	"encoding/json"
	"strings"

	"github.com/Ady-JK/paladin/core/internal/transcript"
)

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

// SessionInfo mirrors the Rust wire shape (snake_case).
type SessionInfo struct {
	CreatedAt    int64  `json:"created_at"`
	Cwd          string `json:"cwd"`
	MessageCount int    `json:"message_count"`
	Model        string `json:"model"`
	Provider     string `json:"provider"`
	SessionID    string `json:"session_id"`
	Title        string `json:"title"`
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
	}
}

// HistoryMessage is the client-facing history row: tool rows link back
// to their call; reminders keep their job link instead.
type HistoryMessage struct {
	Arguments  *string `json:"arguments,omitempty"`
	Content    string  `json:"content"`
	CreatedAt  int64   `json:"createdAt"`
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
	for _, msg := range msgs {
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
		out = append(out, HistoryMessage{
			Role:      role,
			Content:   msg.Content,
			CreatedAt: msg.CreatedAt.Unix(),
		})
	}
	if out == nil {
		out = []HistoryMessage{}
	}
	return out
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
