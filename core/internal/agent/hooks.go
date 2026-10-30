package agent

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Ady-JK/paladin/core/internal/hooks"
	"github.com/Ady-JK/paladin/core/internal/inference"
	"github.com/Ady-JK/paladin/core/internal/jsonx"
	"github.com/Ady-JK/paladin/core/internal/tools"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

// maxStopContinuations bounds how often Stop hooks may keep one turn
// going, so a hook that always blocks cannot loop forever.
const maxStopContinuations = 8

// hookHalt records a hook's continue:false; the loop ends the turn at
// the next safe point (after the current tool batch).
type hookHalt struct {
	mu     sync.Mutex
	set    bool
	reason string
}

func (h *hookHalt) trip(reason string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.set {
		h.set, h.reason = true, reason
	}
}

func (h *hookHalt) tripped() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.set
}

func (a *Agent) hookInput(ev hooks.Event) hooks.Input {
	in := hooks.Input{
		Event:          ev,
		SessionID:      a.session.Meta.SessionID,
		Cwd:            a.toolCtx.Workspace,
		PermissionMode: a.permissionMode,
	}
	if a.session.Dir != "" {
		in.TranscriptPath = filepath.Join(a.session.Dir, "messages.jsonl")
	}
	if a.provider != nil {
		in.Model = a.provider.Model()
	}
	return in
}

// runHooks runs one event's hooks, reports what they did to the UI and
// notes a continue:false. Safe to call from parallel tool goroutines.
func (a *Agent) runHooks(in hooks.Input) hooks.Outcome {
	if !a.hooks.Has(in.Event) {
		return hooks.Outcome{}
	}
	out := a.hooks.Run(a.ctx, in)
	for i := range out.Notes {
		note := out.Notes[i]
		_ = a.emit(ChatChunk{Hook: &note})
	}
	if out.Stop {
		a.halt.trip(out.StopReason)
	}
	return out
}

func hookContext(ev hooks.Event, ctx []string) string {
	if len(ctx) == 0 {
		return ""
	}
	return "\n\n<hook_context event=\"" + string(ev) + "\">\n" + strings.Join(ctx, "\n\n") + "\n</hook_context>"
}

// runPromptHooks runs SessionStart (first turn) and UserPromptSubmit
// before the message reaches the model. Their context joins the stored
// message; the UI keeps showing what the user typed.
func (a *Agent) runPromptHooks() error {
	original := a.currentMessage
	var extra string
	if a.sessionStart != "" {
		in := a.hookInput(hooks.SessionStart)
		in.Source = a.sessionStart
		out := a.runHooks(in)
		extra += hookContext(hooks.SessionStart, out.Context)
	}
	in := a.hookInput(hooks.UserPromptSubmit)
	in.Prompt = original
	out := a.runHooks(in)
	if out.Blocked || out.Stop {
		reason := out.Reason
		if reason == "" {
			reason = out.StopReason
		}
		if reason == "" {
			reason = "no reason given"
		}
		return turnFailed{"prompt blocked by a UserPromptSubmit hook: " + reason}
	}
	extra += hookContext(hooks.UserPromptSubmit, out.Context)
	if extra == "" {
		return nil
	}
	if strings.TrimSpace(a.currentDisplay) == "" {
		a.currentDisplay = original
	}
	a.currentMessage = original + extra
	return nil
}

// preToolHooks runs PreToolUse for one call. It returns a refusal when
// a hook blocked the call; otherwise it may rewrite the call's input
// and returns the hook's allow/ask verdict for the permission gate.
func (a *Agent) preToolHooks(call *tools.ToolCall) (verdict hooks.Decision, context string, refusal *tools.ToolResult) {
	in := a.hookInput(hooks.PreToolUse)
	in.ToolName, in.ToolUseID = call.Name, call.ID
	in.ToolInput = rawJSON(call.Arguments)
	out := a.runHooks(in)
	if out.Blocked {
		msg := "blocked by a PreToolUse hook: " + out.Reason + ". Do not retry it unchanged."
		return out.Decision, "", &tools.ToolResult{CallID: call.ID, Name: call.Name, IsError: true,
			Content: string(jsonx.MustMarshal(map[string]string{"error": msg}))}
	}
	if out.UpdatedInput != nil {
		call.Arguments = string(out.UpdatedInput)
	}
	return out.Decision, hookContext(hooks.PreToolUse, out.Context), nil
}

// postToolHooks runs PostToolUse and folds its feedback into the result
// the model sees. The tool already ran, so a block is feedback only.
func (a *Agent) postToolHooks(call *tools.ToolCall, result tools.ToolResult) tools.ToolResult {
	in := a.hookInput(hooks.PostToolUse)
	in.ToolName, in.ToolUseID = call.Name, call.ID
	in.ToolInput = rawJSON(call.Arguments)
	in.ToolResponse = rawJSON(result.Content)
	out := a.runHooks(in)
	if out.Blocked {
		result.Content += "\n\n<hook_feedback event=\"PostToolUse\">\n" + out.Reason + "\n</hook_feedback>"
	}
	result.Content += hookContext(hooks.PostToolUse, out.Context)
	return result
}

// stopHooks asks Stop hooks whether the agent may end the turn. A block
// adds the hook's reason as a user message and the loop goes on.
func (a *Agent) stopHooks(text string) (continueTurn bool) {
	if !a.hooks.Has(hooks.Stop) || a.halt.tripped() {
		return false
	}
	in := a.hookInput(hooks.Stop)
	in.LastAssistantMessage = text
	in.StopHookActive = a.stopContinuations > 0
	out := a.runHooks(in)
	if !out.Blocked || out.Stop {
		return false
	}
	if a.stopContinuations >= maxStopContinuations {
		_ = a.emit(ChatChunk{Hook: &hooks.Note{Event: hooks.Stop, Outcome: hooks.NoteError,
			Message: "Stop hooks kept the turn going too many times; ending it"}})
		return false
	}
	a.stopContinuations++
	content := "Stop hook feedback:\n" + out.Reason
	a.messages = append(a.messages, inference.LlmMessage{Role: "user", Content: content})
	rec := transcript.UserMessage(content)
	rec.Display = "Stop hook: " + out.Reason
	a.records = append(a.records, rec)
	return true
}

// rawJSON passes JSON through as-is and wraps anything else as a string.
func rawJSON(s string) json.RawMessage {
	if json.Valid([]byte(s)) {
		return json.RawMessage(s)
	}
	return jsonx.MustMarshal(s)
}
