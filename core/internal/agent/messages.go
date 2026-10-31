// messages.go shapes the on-disk transcript into the
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

// AbortedToolContent is the model-facing result for a tool call that
// never produced one (stop mid-tools, crash, interrupted persist).
// Providers reject an assistant tool call without a matching result,
// so history repair answers it with this instead of failing the turn.
const AbortedToolContent = `{"error":"aborted: this tool call did not complete (the turn was stopped or interrupted); its result is unknown"}`

// LlmMessages builds the LLM view of the stored history. Tool traffic
// only travels with tool-capable providers. Every assistant tool call is
// answered by exactly one tool message directly after it: missing
// answers are synthesized as aborted, duplicates and stray results
// (reminders, orphans, empty or unknown ids) become plain user context
// after the block instead of breaking the provider contract. Only the
// part after the last compaction marker is replayed, behind its summary.
func LlmMessages(history []transcript.Message, withTools bool) []inference.LlmMessage {
	out := []inference.LlmMessage{}
	var (
		open     bool
		order    []inference.ToolCall
		answered map[string]bool
		deferred []inference.LlmMessage
	)
	closeBlock := func() {
		if !open {
			return
		}
		for _, call := range order {
			if answered[call.ID] {
				continue
			}
			id, name := call.ID, call.Name
			out = append(out, inference.LlmMessage{Role: "tool", Content: AbortedToolContent, ToolCallID: &id, Name: &name})
		}
		out = append(out, deferred...)
		open, order, answered, deferred = false, nil, nil, nil
	}
	demote := func(content string) {
		msg := inference.LlmMessage{Role: "user", Content: content}
		if open {
			deferred = append(deferred, msg)
			return
		}
		out = append(out, msg)
	}
	// The model view restarts at the newest compaction marker: its
	// summary stands in for everything before it.
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].IsCompaction() {
			out = append(out, inference.LlmMessage{Role: "user", Content: compactionUserContent(history[i].Content)})
			history = history[i+1:]
			break
		}
	}
	for _, msg := range history {
		switch msg.Role {
		case transcript.RoleUser:
			closeBlock()
			out = append(out, inference.LlmMessage{Role: "user", Content: msg.Content})
		case transcript.RoleAssistant:
			closeBlock()
			var calls []inference.ToolCall
			seen := map[string]bool{}
			if withTools && msg.ToolCalls != nil {
				for _, call := range *msg.ToolCalls {
					if call.ID == "" || seen[call.ID] {
						continue
					}
					seen[call.ID] = true
					calls = append(calls, inference.ToolCall{ID: call.ID, Name: call.Name, Arguments: call.Arguments, Signature: call.Signature})
				}
			}
			if strings.TrimSpace(msg.Content) == "" && len(calls) == 0 {
				continue
			}
			out = append(out, inference.LlmMessage{Role: "assistant", Content: msg.Content, ToolCalls: calls})
			if len(calls) > 0 {
				open, order, answered = true, calls, map[string]bool{}
			}
		case transcript.RoleTool:
			if !withTools {
				continue
			}
			id := ""
			if msg.ToolCallID != nil {
				id = *msg.ToolCallID
			}
			if !open || id == "" || session.IsNotifyMessage(msg) || answered[id] || !blockHas(order, id) {
				demote(msg.Content)
				continue
			}
			answered[id] = true
			callID := id
			out = append(out, inference.LlmMessage{
				Role: "tool", Content: msg.Content,
				Image:      msg.Image,
				ToolCallID: &callID, Name: msg.Name,
			})
		case transcript.RoleSystem:
		}
	}
	closeBlock()
	return out
}

func blockHas(calls []inference.ToolCall, id string) bool {
	for _, c := range calls {
		if c.ID == id {
			return true
		}
	}
	return false
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
