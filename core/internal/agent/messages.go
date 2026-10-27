// Package agent: messages.go shapes the on-disk transcript into the
// model-facing message list and renders background-job notices.
package agent

import (
	"encoding/json"
	"strings"

	"github.com/Ady-JK/paladin/core/internal/inference"
	"github.com/Ady-JK/paladin/core/internal/session"
	"github.com/Ady-JK/paladin/core/internal/tools"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

func (a *Agent) modelMessages(message string, resume bool) []inference.LlmMessage {
	withTools := len(a.toolDefs) > 0
	messages := LlmMessages(a.history, withTools)
	if withTools && !resume {
		messages = append(messages, inference.LlmMessage{Role: "user", Content: message})
	}
	return messages
}

// LlmMessages builds the LLM view of the stored history. Tool traffic
// only travels with tool-capable providers; anything that cannot link
// (reminders, orphans, empty ids) becomes plain user context instead of
// breaking the turn.
func LlmMessages(history []transcript.Message, withTools bool) []inference.LlmMessage {
	known := map[string]bool{}
	for _, msg := range history {
		if msg.Role != transcript.RoleAssistant {
			continue
		}
		if msg.ToolCalls != nil {
			for _, call := range *msg.ToolCalls {
				if call.ID != "" {
					known[call.ID] = true
				}
			}
		}
	}
	var out []inference.LlmMessage
	for _, msg := range history {
		msg := msg
		switch msg.Role {
		case transcript.RoleUser:
			out = append(out, inference.LlmMessage{Role: "user", Content: msg.Content})
		case transcript.RoleAssistant:
			var calls []inference.ToolCall
			if withTools && msg.ToolCalls != nil {
				for _, call := range *msg.ToolCalls {
					if call.ID == "" {
						continue
					}
					calls = append(calls, inference.ToolCall{ID: call.ID, Name: call.Name, Arguments: call.Arguments, Signature: call.Signature})
				}
			}
			if strings.TrimSpace(msg.Content) == "" && len(calls) == 0 {
				continue
			}
			out = append(out, inference.LlmMessage{Role: "assistant", Content: msg.Content, ToolCalls: calls})
		case transcript.RoleTool:
			if !withTools {
				continue
			}
			linked := false
			if msg.ToolCallID != nil && *msg.ToolCallID != "" {
				linked = known[*msg.ToolCallID]
			}
			id := ""
			if msg.ToolCallID != nil {
				id = *msg.ToolCallID
			}
			if id == "" || session.IsNotifyMessage(msg) || !linked {
				out = append(out, inference.LlmMessage{Role: "user", Content: msg.Content})
				continue
			}
			out = append(out, inference.LlmMessage{
				Role: "tool", Content: msg.Content,
				ToolCallID: msg.ToolCallID, Name: msg.Name,
			})
		case transcript.RoleSystem:
		}
	}
	if out == nil {
		out = []inference.LlmMessage{}
	}
	return out
}

// notifyContent renders a finished background job as the model-facing
// reminder payload.
func notifyContent(notice *tools.NotifyResult) string {
	out := map[string]any{
		"notice":    "background job finished",
		"job_id":    notice.JobID,
		"status":    notice.Status,
		"exit_code": notice.ExitCode,
		"output":    notice.Output,
		"truncated": notice.Truncated,
	}
	data, err := json.Marshal(out)
	if err != nil {
		return "{}"
	}
	return string(data)
}
