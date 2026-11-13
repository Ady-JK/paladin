// toolcalls.go runs a round's tool calls: batching, permission
// gating, execution and the notices they leave.

package agent

import (
	"sync"

	"github.com/Ady-JK/paladin/core/internal/hooks"
	"github.com/Ady-JK/paladin/core/internal/inference"
	"github.com/Ady-JK/paladin/core/internal/jsonx"
	"github.com/Ady-JK/paladin/core/internal/permission"
	"github.com/Ady-JK/paladin/core/internal/session"
	"github.com/Ady-JK/paladin/core/internal/tools"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

// canonicalizeCalls rewrites each call's name to the tool that will
// really run, so a model asking for "bash" is recorded, announced,
// judged by hooks and the gate, and replayed to the model as "shell".
//
// It runs once per round, before appendAssistant, because that
// assistant message and the execution below share the same call
// slice: renaming in place fixes the stored conversation (and so every
// later round's replay) as well as this round's events in one place.
// The call id is untouched — it is what pairs a call with its result.
func canonicalizeCalls(reg *tools.Registry, calls []inference.ToolCall) {
	for i := range calls {
		calls[i].Name = reg.CanonicalName(calls[i].Name)
	}
}

func (a *Agent) runToolsStreaming(calls []inference.ToolCall) error {
	// Journal every request before executing any of them.
	if err := a.checkpoint(""); err != nil {
		a.settleAborted(calls)
		return err
	}
	if a.noteReadOnlyRound(calls) {
		return a.refuseReadOnlyCalls(calls)
	}
	a.settleNotices(calls)
	if err := a.announceToolCalls(calls); err != nil {
		return err
	}
	return a.runToolBatches(calls)
}

func (a *Agent) refuseReadOnlyCalls(calls []inference.ToolCall) error {
	for i := range calls {
		result := tools.ToolResult{CallID: calls[i].ID, Name: calls[i].Name, IsError: true,
			Content: readOnlyRefusal(a.readOnlyRounds)}
		a.appendToolResult(&calls[i], &result)
		if err := a.checkpoint(""); err != nil {
			a.settleAborted(calls[i+1:])
			return err
		}
		if err := a.emit(ChatChunk{Tool: toolActivity(&calls[i], &result)}); err != nil {
			return turnStop{}
		}
	}
	return nil
}

func (a *Agent) announceToolCalls(calls []inference.ToolCall) error {
	for i, call := range calls {
		if err := a.emit(ChatChunk{Tool: &ToolActivity{
			ID: call.ID, Name: call.Name, Arguments: call.Arguments,
			Status: session.ToolRunning,
		}}); err != nil {
			a.settleAborted(calls[i:])
			return turnStop{}
		}
	}
	return nil
}

func (a *Agent) runToolBatches(calls []inference.ToolCall) error {
	for start := 0; start < len(calls); {
		if a.stopRequested() {
			a.settleAborted(calls[start:])
			return turnStop{}
		}
		end := a.batchEnd(calls, start)
		results, journalErr := a.execBatch(calls[start:end])
		a.recordToolBatch(calls[start:end], results)
		if journalErr != nil {
			a.settleAborted(calls[end:])
			return journalErr
		}
		if err := a.checkpoint(""); err != nil {
			a.settleAborted(calls[end:])
			return err
		}
		if err := a.emitToolBatch(calls[start:end], results, calls[end:]); err != nil {
			return err
		}
		start = end
	}
	return nil
}

func (a *Agent) recordToolBatch(calls []inference.ToolCall, results []tools.ToolResult) {
	for i, call := range calls {
		a.noteEditResult(&call, &results[i])
		result := results[i]
		if result.IsError {
			a.logErrorf("tool:"+result.Name, "%s %s\n%s", call.ID, call.Arguments, result.Content)
		}
		a.appendToolResult(&call, &result)
	}
}

func (a *Agent) emitToolBatch(calls []inference.ToolCall, results []tools.ToolResult, remaining []inference.ToolCall) error {
	for i, call := range calls {
		result := results[i]
		if err := a.emit(ChatChunk{Tool: toolActivity(&call, &result)}); err != nil {
			a.settleAborted(remaining)
			return turnStop{}
		}
		if a.noteDenial(&result) {
			a.settleAborted(remaining)
			return turnFailed{msg: "stopped after 3 consecutive denied tool calls — the approach needs approval that is not coming; change approach or ask the user what they want instead"}
		}
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
func (a *Agent) execBatch(batch []inference.ToolCall) ([]tools.ToolResult, error) {
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
		return results, nil
	}
	var wg sync.WaitGroup
	journal := batchJournal{agent: a, results: results, completed: make([]bool, len(batch))}
	for i := range batch {
		if run[i] {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				exec(i)
				journal.complete(i)
			}(i)
		}
	}
	wg.Wait()
	return results, journal.err
}

type batchJournal struct {
	mu        sync.Mutex
	agent     *Agent
	results   []tools.ToolResult
	completed []bool
	err       error
}

func (j *batchJournal) complete(index int) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.completed[index] = true
	if j.err != nil {
		return
	}
	var extra []transcript.Message
	for i, done := range j.completed {
		if !done {
			continue
		}
		result := j.results[i]
		id, name := result.CallID, result.Name
		extra = append(extra, transcript.Message{Role: transcript.RoleTool, Content: result.Content, Image: result.Image, ToolCallID: &id, Name: &name, IsError: result.IsError})
	}
	j.err = j.agent.checkpointWithResults("", extra)
}

// callGated runs one call through the permission gate (after a hold for
// unseen folder instructions): refused calls
// never reach the tool, approved shell calls run in the gate's sandbox,
// and a sandbox block may be escalated to a rerun, confined with the
// blocked folder granted or (once) unconfined.
func (a *Agent) callGated(call *tools.ToolCall) tools.ToolResult {
	// Unseen folder instructions come first: the call is not run, so it
	// skips hooks and the gate, and the resent call goes through both.
	if held, ok := a.tools.HoldForInstructions(call, a.toolCtx); ok {
		return held
	}
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
	if pt, ok := a.gate.(ProjectTrust); ok {
		ctx.TrustProject = func(tool string) bool { return pt.TrustProject(a.ctx, call.ID, tool, notify) }
	}
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

// callTool runs the tool itself. Approval waits stay outside it.
func (a *Agent) callTool(call *tools.ToolCall, ctx *tools.ToolCtx) tools.ToolResult {
	return a.tools.Call(call, ctx)
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
