// loop.go is the per-turn execution loop. It streams one
// model round, runs any requested tools, appends the results, and
// repeats until the model stops asking for tools or the round cap is hit.

package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/Ady-JK/paladin/core/internal/hooks"
	"github.com/Ady-JK/paladin/core/internal/inference"
	"github.com/Ady-JK/paladin/core/internal/jsonx"
	"github.com/Ady-JK/paladin/core/internal/permission"
	"github.com/Ady-JK/paladin/core/internal/session"
	"github.com/Ady-JK/paladin/core/internal/tools"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

func (a *Agent) runLoop() error {
	if len(a.toolDefs) == 0 {
		return a.runPlain()
	}
	// No round cap: like Codex and OpenCode, only the model (a reply
	// without tool calls), the user, or an unrecoverable error ends a turn.
	for {
		a.round++
		if a.stopRequested() {
			return turnStop{}
		}
		if end, err := a.checkBudget(); end || err != nil {
			return err
		}
		if err := a.drainNotifications(); err != nil {
			return err
		}
		if err := a.drainSteering(); err != nil {
			return err
		}
		a.settleNotices(nil)
		a.maybeCompact()
		if a.stopRequested() {
			return turnStop{}
		}
		round := a.streamRound()
		if a.stopRequested() {
			return a.stopWithPartial(round.text)
		}
		if err := round.providerErr; err != nil {
			if inference.IsCancelled(err) || a.stopRequested() {
				return a.stopWithPartial(round.text)
			}
			if a.recoverRound(err, round.empty()) {
				continue
			}
			return roundFailure(err)
		}
		if round.clientGone {
			return a.stopWithPartial(round.text)
		}
		if err := round.streamErr; err != nil {
			if a.recoverRound(err, round.empty()) {
				continue
			}
			return roundFailure(err)
		}
		a.noteRoundSucceeded()
		// Empty stream fallback: one buffered completion. A round that
		// yields neither text nor tool calls is usually a gateway blip, so
		// retry before believing it; three strikes ends the turn. Ending
		// silent would fake a READY with nothing done (the "random stop"
		// that forces another nudge), so a turn with zero progress fails
		// loudly instead.
		if round.empty() {
			if a.emptyRounds >= 2 {
				if len(a.records) > 0 || a.reply.Len() > 0 {
					a.finish(phaseDone)
					return nil
				}
				return turnFailed{"model returned an empty response \u2014 try again"}
			}
			a.emptyRounds++
			retry, err := a.completeBuffered(&round)
			if err != nil {
				return err
			}
			// Still empty after the buffered fallback: retry next round
			// instead of ending the turn silent (bounded by emptyRounds).
			if retry || round.empty() {
				continue
			}
		}
		// The budget wrap-up has no tools; a call it hallucinates anyway
		// is dropped and its text ends the turn.
		if len(round.calls) == 0 || a.budgetWrapUp {
			more, err := a.finishTextRound(round.text)
			if more {
				continue
			}
			return err
		}
		a.emptyRounds = 0
		a.retries, a.timeoutRetries = 0, 0
		a.appendAssistant(round.text, round.calls)
		if err := a.runToolsStreaming(round.calls); err != nil {
			return err
		}
		if a.halt.tripped() {
			a.finish(phaseDone)
			return nil
		}
	}
}

// roundResult is what one streamed model round produced.
type roundResult struct {
	text  string
	calls []inference.ToolCall
	// providerErr is the provider call's own error; streamErr an error
	// chunk it streamed.
	providerErr error
	streamErr   error
	// clientGone: the turn's client stopped listening mid-round.
	clientGone bool
}

func (r roundResult) empty() bool { return r.text == "" && len(r.calls) == 0 }

// streamRound streams one model round, forwarding text and tool notices
// to the client as they arrive.
func (a *Agent) streamRound() roundResult {
	a.sentLen = len(a.messages)
	a.publishLive()
	prx := make(chan inference.Chunk, 64)
	providerErr := make(chan error, 1)
	go func(msgs []inference.LlmMessage, defs []inference.ToolDefinition, sid string) {
		defer close(prx)
		providerErr <- guardProvider(func() error {
			return a.provider.StreamTools(a.ctx, a.system, msgs, defs, sid, prx, a.done)
		})
	}(append([]inference.LlmMessage(nil), a.messages...), append([]inference.ToolDefinition(nil), a.toolDefs...), a.session.Meta.SessionID)
	var res roundResult
	var text strings.Builder
loop:
	for {
		select {
		case chunk, ok := <-prx:
			if !ok {
				break loop
			}
			if chunk.Err != "" {
				res.streamErr = errors.New(chunk.Err)
				break loop
			}
			if chunk.Usage != nil {
				a.noteCallUsage(*chunk.Usage)
			}
			if notice := chunk.ToolNotice; notice != nil {
				name := notice.Name
				if name == "" {
					name = "tool"
				}
				a.noteNotice(notice.ID, name)
				if err := a.emit(ChatChunk{Tool: &ToolActivity{
					ID: notice.ID, Name: name, Arguments: "{}",
					Status: session.ToolRunning,
				}}); err != nil {
					res.clientGone = true
					break loop
				}
			}
			if chunk.Text != "" {
				text.WriteString(chunk.Text)
				a.appendDelta(chunk.Text)
				if err := a.emit(ChatChunk{Delta: chunk.Text}); err != nil {
					res.clientGone = true
					break loop
				}
			}
			if len(chunk.ToolCalls) > 0 {
				res.calls = chunk.ToolCalls
			}
			if a.stopRequested() {
				break loop
			}
		case <-a.ctx.Done():
			break loop
		case <-a.done:
			res.clientGone = true
			break loop
		}
	}
	res.providerErr = <-providerErr
	res.text = text.String()
	return res
}

// completeBuffered retries an empty streamed round as one buffered
// completion, filling round in. retry asks the loop to start the next
// round; err ends the turn.
func (a *Agent) completeBuffered(round *roundResult) (retry bool, err error) {
	resp, err := a.provider.CompleteTools(a.ctx, a.system, a.messages, a.toolDefs, a.session.Meta.SessionID)
	if err != nil {
		if a.stopRequested() {
			return false, a.stopWithPartial(round.text)
		}
		if a.recoverRound(err, true) {
			return true, nil
		}
		return false, roundFailure(err)
	}
	round.text += resp.Content
	a.appendDelta(resp.Content)
	round.calls = resp.ToolCalls
	if resp.Usage != nil {
		a.noteCallUsage(*resp.Usage)
	}
	if resp.Content != "" {
		if err := a.emit(ChatChunk{Delta: resp.Content}); err != nil {
			return false, a.stopWithPartial(round.text)
		}
	}
	return false, nil
}

// finishTextRound settles a round that answered without tool calls. The
// turn ends there unless background shell jobs are still due to report
// (more: run another round with their results).
func (a *Agent) finishTextRound(text string) (more bool, err error) {
	if text != "" {
		a.emptyRounds = 0
		a.retries, a.timeoutRetries = 0, 0
		// The model must see its own answer if the turn goes on (a steer,
		// a job notice or a Stop hook).
		a.messages = append(a.messages, inference.LlmMessage{Role: "assistant", Content: text})
		a.records = append(a.records, transcript.Message{
			Role: transcript.RoleAssistant, Content: text,
			CreatedAt: time.Now().UTC(),
		})
	}
	sid := a.session.Meta.SessionID
	// The wait only fails when the turn is stopped; text is already recorded.
	if a.shell != nil && a.shell.PendingNotify(sid) && !a.shell.WaitPendingNotify(sid, a.ctx) {
		return false, turnStop{}
	}
	drained := len(a.records)
	if err := a.drainNotifications(); err != nil {
		return false, err
	}
	if err := a.drainSteering(); err != nil {
		return false, err
	}
	if len(a.records) != drained {
		return true, nil
	}
	if a.stopRequested() {
		return false, turnStop{}
	}
	if a.stopHooks(text) {
		return true, nil
	}
	a.finish(phaseDone)
	return false, nil
}

func (a *Agent) stopWithPartial(roundText string) error {
	if roundText != "" {
		a.records = append(a.records, transcript.Message{
			Role: transcript.RoleAssistant, Content: roundText,
			CreatedAt: time.Now().UTC(),
		})
	}
	return turnStop{}
}

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

// maxParallelTools caps one concurrent batch.
const maxParallelTools = 8

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

// maxEscalations bounds sandbox-block prompts per call: each granted
// folder reruns the command, which may then hit the next folder.
const maxEscalations = 3

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
	return a.steps.Track(call.ID, call.Name, func() tools.ToolResult { return a.tools.Call(call, ctx) })
}

// settleAborted answers calls that will never run so the transcript
// keeps every assistant tool call paired with a result. The UI rows are
// settled best-effort; the persisted records are what matter.
func (a *Agent) settleAborted(calls []inference.ToolCall) {
	for _, call := range calls {
		call := call
		result := tools.ToolResult{CallID: call.ID, Name: call.Name, Content: AbortedToolContent, IsError: true}
		a.appendToolResult(&call, &result)
		select {
		case <-a.done:
		default:
			_ = a.emit(ChatChunk{Tool: toolActivity(&call, &result)})
		}
	}
}

// NoticeInterruptedContent is the row result for a tool call the model
// announced but never finished sending (stop, failure or retry).
const NoticeInterruptedContent = `{"error":"interrupted: the model's tool call never completed, so it did not run"}`

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

func (a *Agent) drainNotifications() error {
	if a.shell == nil {
		return nil
	}
	for _, notice := range a.shell.PollNotify(a.session.Meta.SessionID) {
		content := notifyContent(&notice)
		callID := session.NotifyCallID(notice.CallID)
		a.appendNotify(content, callID, notice.IsError)
		status := session.ToolOK
		if notice.IsError {
			status = session.ToolFailed
		}
		if err := a.emit(ChatChunk{Tool: &ToolActivity{
			ID: callID, Name: "shell",
			Arguments: session.NotifyArgs(notice.JobID),
			Result:    content, Status: status,
		}}); err != nil {
			return turnStop{}
		}
	}
	return nil
}

// drainSteering moves messages the user sent mid-turn into the
// conversation as user messages, ahead of the next model call, and
// echoes each so live clients can show where it landed.
func (a *Agent) drainSteering() error {
	// Steers during the budget wrap-up wait for a later turn.
	if a.steer == nil || a.budgetWrapUp {
		return nil
	}
	for _, msg := range a.steer() {
		if strings.TrimSpace(msg.Message) == "" {
			continue
		}
		a.messages = append(a.messages, inference.LlmMessage{Role: "user", Content: msg.Message})
		rec := transcript.UserMessage(msg.Message)
		if d := strings.TrimSpace(msg.Display); d != "" && d != strings.TrimSpace(msg.Message) {
			rec.Display = msg.Display
		}
		a.records = append(a.records, rec)
		if err := a.emit(ChatChunk{Steer: &msg}); err != nil {
			return turnStop{}
		}
	}
	return nil
}

// runPlain is the plain completion path for a provider with no tool defs.
func (a *Agent) runPlain() error {
	if !a.budgetChecked {
		if end, err := a.checkBudget(); end || err != nil {
			return err
		}
	}
	prx := make(chan inference.Chunk, 64)
	providerErr := make(chan error, 1)
	go func() {
		defer close(prx)
		msgs := append([]inference.LlmMessage(nil), a.messages...)
		providerErr <- guardProvider(func() error {
			return a.provider.StreamTools(a.ctx, a.system, msgs, nil, a.session.Meta.SessionID, prx, a.done)
		})
	}()
	streamed := false
loop:
	for {
		select {
		case chunk, ok := <-prx:
			if !ok {
				break loop
			}
			if chunk.Err != "" {
				if a.stopRequested() {
					return turnStop{}
				}
				if !streamed && a.retryRound(errors.New(chunk.Err), true) {
					return a.runPlain()
				}
				return turnFailed{"provider unavailable: " + chunk.Err}
			}
			if chunk.Text != "" {
				streamed = true
				a.appendDelta(chunk.Text)
				if err := a.emit(ChatChunk{Delta: chunk.Text}); err != nil {
					return turnStop{}
				}
			}
			if chunk.Usage != nil {
				a.noteCallUsage(*chunk.Usage)
			}
			if a.stopRequested() {
				return turnStop{}
			}
		case <-a.ctx.Done():
			return turnStop{}
		case <-a.done:
			return turnStop{}
		}
	}
	if err := <-providerErr; err != nil {
		if a.stopRequested() {
			return turnStop{}
		}
		_ = streamed
		if inference.IsCancelled(err) || a.stopRequested() {
			return turnStop{}
		}
		if !streamed && a.retryRound(err, true) {
			return a.runPlain()
		}
		return turnFailed{"provider unavailable: " + err.Error()}
	}
	if a.stopRequested() {
		return turnStop{}
	}
	if !streamed {
		resp, err := a.provider.CompleteTools(a.ctx, a.system, a.messages, nil, a.session.Meta.SessionID)
		if err != nil {
			if a.stopRequested() {
				return turnStop{}
			}
			if !streamed && a.retryRound(err, true) {
				return a.runPlain()
			}
			return turnFailed{"provider unavailable: " + err.Error()}
		}
		a.appendDelta(resp.Content)
		if resp.Usage != nil {
			a.noteCallUsage(*resp.Usage)
		}
		content := a.reply.String()
		if err := a.emit(ChatChunk{Delta: content}); err != nil {
			return turnStop{}
		}
	}
	a.finish(phaseDone)
	return nil
}

// guardProvider runs a provider call on its own goroutine's behalf: a
// panic there would bypass every recover and kill the sidecar, so it
// becomes a non-retryable provider error instead.
func guardProvider(call func() error) (err error) {
	defer func() {
		if p := recover(); p != nil {
			fmt.Fprintf(os.Stderr, "provider panicked: %v\n%s", p, debug.Stack())
			err = fmt.Errorf("provider crashed: %v", p)
		}
	}()
	return call()
}
