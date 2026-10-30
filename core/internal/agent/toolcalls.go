// toolcalls.go runs a round's tool calls: batching, permission
// gating, execution and the notices they leave.

package agent

import (
	"encoding/json"
	"sync"

	"github.com/Ady-JK/paladin/core/internal/hooks"
	"github.com/Ady-JK/paladin/core/internal/inference"
	"github.com/Ady-JK/paladin/core/internal/jsonx"
	"github.com/Ady-JK/paladin/core/internal/permission"
	"github.com/Ady-JK/paladin/core/internal/session"
	"github.com/Ady-JK/paladin/core/internal/tools"
)

func (a *Agent) runToolsStreaming(calls []inference.ToolCall) error {
	a.settleNotices(calls)
	for i, call := range calls {
		if err := a.emit(ChatChunk{Tool: &ToolActivity{
			ID: call.ID, Name: call.Name, Arguments: call.Arguments,
			Status: session.ToolRunning,
		}}); err != nil {
			a.settleAborted(calls[i:])
			return turnStop{}
		}
	}
	for i := 0; i < len(calls); {
		if a.stopRequested() {
			a.settleAborted(calls[i:])
			return turnStop{}
		}
		j := a.batchEnd(calls, i)
		results := a.execBatch(calls[i:j])
		for k := i; k < j; k++ {
			call, result := calls[k], results[k-i]
			a.armNotify(&call, &result)
			a.appendToolResult(&call, &result)
			if err := a.emit(ChatChunk{Tool: toolActivity(&call, &result)}); err != nil {
				a.settleAborted(calls[k+1:])
				return turnStop{}
			}
		}
		i = j
	}
	return nil
}

// batchEnd returns the end of the batch starting at i: a run of
// consecutive parallel-safe calls, or the single exclusive call at i.
// Exclusive calls are barriers, so the model's call order is preserved
// for anything with side effects.
func (a *Agent) batchEnd(calls []inference.ToolCall, i int) int {
	j := i + 1
	if !a.tools.ParallelSafe(calls[i].Name) {
		return j
	}
	for j < len(calls) && j-i < maxParallelTools && a.tools.ParallelSafe(calls[j].Name) {
		j++
	}
	return j
}

// execBatch runs one batch and returns results in call order. Doom
// checks stay sequential so their counting matches the call order.
func (a *Agent) execBatch(batch []inference.ToolCall) []tools.ToolResult {
	results := make([]tools.ToolResult, len(batch))
	run := make([]bool, len(batch))
	for i, call := range batch {
		if refusal := a.doom.check(call.Name, call.Arguments); refusal != "" {
			results[i] = tools.ToolResult{CallID: call.ID, Name: call.Name, IsError: true,
				Content: string(jsonx.MustMarshal(map[string]string{"error": refusal}))}
			continue
		}
		run[i] = true
	}
	exec := func(i int) {
		c := batch[i]
		results[i] = a.callGated(&tools.ToolCall{ID: c.ID, Name: c.Name, Arguments: c.Arguments})
	}
	if len(batch) == 1 {
		if run[0] {
			exec(0)
		}
		return results
	}
	var wg sync.WaitGroup
	for i := range batch {
		if run[i] {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				exec(i)
			}(i)
		}
	}
	wg.Wait()
	return results
}

// callGated runs one call through the permission gate: refused calls
// never reach the tool, approved shell calls run in the gate's sandbox,
// and a sandbox block may be escalated to a rerun, confined with the
// blocked folder granted or (once) unconfined.
func (a *Agent) callGated(call *tools.ToolCall) tools.ToolResult {
	if !a.hooks.Has(hooks.PreToolUse) && !a.hooks.Has(hooks.PostToolUse) {
		return a.authorizedCall(call, hooks.DecisionNone)
	}
	verdict, context, refusal := a.preToolHooks(call)
	if refusal != nil {
		return *refusal
	}
	result := a.authorizedCall(call, verdict)
	result.Content += context
	return a.postToolHooks(call, result)
}

// authorizedCall runs one call through the permission gate. verdict is
// a PreToolUse hook's allow/ask, which may skip or force the prompt.
func (a *Agent) authorizedCall(call *tools.ToolCall, verdict hooks.Decision) tools.ToolResult {
	if a.gate == nil {
		return a.callTool(call, a.toolCtx)
	}
	name, args, ok := a.tools.Prepare(call)
	if !ok {
		return a.callTool(call, a.toolCtx)
	}
	notify := func(ap permission.Approval) { _ = a.emit(ChatChunk{Approval: &ap}) }
	auth := a.gate.Authorize(permission.WithHookVerdict(a.ctx, string(verdict)), call.ID, name, args, notify)
	if !auth.Run {
		return tools.ToolResult{CallID: call.ID, Name: name, IsError: true, Content: auth.Content}
	}
	ctx := *a.toolCtx
	ctx.Sandbox = auth.Sandbox
	result := a.callTool(call, &ctx)
	for i := 0; i < maxEscalations && ctx.Sandbox != nil && result.IsError; i++ {
		output, blocked := tools.SandboxBlocked(result.Content)
		if !blocked {
			break
		}
		esc := a.gate.Escalate(a.ctx, call.ID, name, args, permission.Blocked{Policy: ctx.Sandbox, Output: output}, notify)
		if !esc.Run {
			break
		}
		ctx.Sandbox = esc.Sandbox
		result = a.callTool(call, &ctx)
	}
	return result
}

// callTool runs the tool itself, bracketed by the step recorder when it
// may change files. Approval waits stay outside the bracket.
func (a *Agent) callTool(call *tools.ToolCall, ctx *tools.ToolCtx) tools.ToolResult {
	if a.steps == nil || !a.tools.Mutates(call.Name) {
		return a.tools.Call(call, ctx)
	}
	return a.steps.Track(a.ctx, call.ID, call.Name, func() tools.ToolResult { return a.tools.Call(call, ctx) })
}

// settleAborted answers calls that will never run so the transcript
// keeps every assistant tool call paired with a result. The UI rows are
// settled best-effort; the persisted records are what matter.
func (a *Agent) settleAborted(calls []inference.ToolCall) {
	for _, call := range calls {
		result := tools.ToolResult{CallID: call.ID, Name: call.Name, Content: AbortedToolContent, IsError: true}
		a.appendToolResult(&call, &result)
		select {
		case <-a.done:
		default:
			_ = a.emit(ChatChunk{Tool: toolActivity(&call, &result)})
		}
	}
}

func (a *Agent) noteNotice(id, name string) {
	if id == "" {
		return
	}
	for _, n := range a.noticed {
		if n.ID == id {
			return
		}
	}
	a.noticed = append(a.noticed, inference.ToolCall{ID: id, Name: name})
}

// settleNotices fails every announced row not among calls (the round's
// final tool calls, which settle on their own) and clears the list.
func (a *Agent) settleNotices(calls []inference.ToolCall) {
	pending := a.noticed
	a.noticed = nil
	for _, n := range pending {
		if blockHas(calls, n.ID) {
			continue
		}
		select {
		case <-a.done:
			return
		default:
		}
		_ = a.emit(ChatChunk{Tool: &ToolActivity{
			ID: n.ID, Name: n.Name, Arguments: "{}",
			Result: NoticeInterruptedContent, Status: session.ToolFailed,
		}})
	}
}

func toolActivity(call *inference.ToolCall, result *tools.ToolResult) *ToolActivity {
	status := session.ToolOK
	if result.IsError {
		status = session.ToolFailed
	}
	return &ToolActivity{
		ID: call.ID, Name: call.Name, Arguments: call.Arguments,
		Result: result.Content, Status: status,
	}
}

func (a *Agent) armNotify(call *inference.ToolCall, result *tools.ToolResult) {
	if a.shell == nil || result.Name != "shell" || result.IsError {
		return
	}
	var args struct {
		Action     string `json:"action"`
		Background bool   `json:"background"`
		Notify     bool   `json:"notify"`
	}
	if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
		return
	}
	if (args.Action != "" && args.Action != "run") || !args.Background || !args.Notify {
		return
	}
	var receipt struct {
		JobID  string `json:"job_id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(result.Content), &receipt); err != nil {
		return
	}
	if receipt.JobID == "" || receipt.Status != tools.JobRunning {
		return
	}
	a.shell.ArmNotify(a.session.Meta.SessionID, call.ID, receipt.JobID)
}
