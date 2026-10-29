// Package agent: loop.go is the per-turn execution loop. It streams one
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

	"github.com/Ady-JK/paladin/core/internal/inference"
	"github.com/Ady-JK/paladin/core/internal/jsonx"
	"github.com/Ady-JK/paladin/core/internal/session"
	"github.com/Ady-JK/paladin/core/internal/tools"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

func (a *Agent) runLoop() error {
	if len(a.toolDefs) == 0 {
		return a.runPlain()
	}
	for !a.needsContinue() {
		a.round++
		if a.stopRequested() {
			return turnStop{}
		}
		if err := a.drainNotifications(); err != nil {
			return err
		}
		a.settleNotices(nil)
		a.maybeCompact()
		if a.stopRequested() {
			return turnStop{}
		}
		a.sentLen = len(a.messages)
		prx := make(chan inference.Chunk, 64)
		providerErr := make(chan error, 1)
		go func(msgs []inference.LlmMessage, defs []inference.ToolDefinition, sid string) {
			defer close(prx)
			providerErr <- guardProvider(func() error {
				return a.provider.StreamTools(a.ctx, a.system, msgs, defs, sid, prx, a.done)
			})
		}(append([]inference.LlmMessage(nil), a.messages...), append([]inference.ToolDefinition(nil), a.toolDefs...), a.session.Meta.SessionID)
		var roundText strings.Builder
		var calls []inference.ToolCall
		var streamErr *string
	loop:
		for {
			select {
			case chunk, ok := <-prx:
				if !ok {
					break loop
				}
				if chunk.Err != "" {
					streamErr = &chunk.Err
					break loop
				}
				if chunk.Usage != nil {
					a.noteCallUsage(*chunk.Usage)
				}
				if chunk.ToolNotice != nil {
					notice := chunk.ToolNotice
					name := notice.Name
					if name == "" {
						name = "tool"
					}
					a.noteNotice(notice.ID, name)
					if err := a.emit(ChatChunk{Tool: &ToolActivity{
						ID: notice.ID, Name: name, Arguments: "{}",
						Status: session.ToolRunning,
					}}); err != nil {
						se := "client went away"
						streamErr = &se
						break loop
					}
				}
				if chunk.Text != "" {
					roundText.WriteString(chunk.Text)
					a.appendDelta(chunk.Text)
					if err := a.emit(ChatChunk{Delta: chunk.Text}); err != nil {
						se := "client went away"
						streamErr = &se
						break loop
					}
				}
				if len(chunk.ToolCalls) > 0 {
					calls = chunk.ToolCalls
				}
				if a.stopRequested() {
					break loop
				}
			case <-a.ctx.Done():
				break loop
			case <-a.done:
				se := "client went away"
				streamErr = &se
				break loop
			}
		}
		taskErr := <-providerErr
		if a.stopRequested() {
			return a.stopWithPartial(roundText.String())
		}
		if taskErr != nil {
			if inference.IsCancelled(taskErr) || a.stopRequested() {
				return a.stopWithPartial(roundText.String())
			}
			if a.recoverRound(taskErr, roundText.Len() == 0 && len(calls) == 0) {
				continue
			}
			return roundFailure(taskErr)
		}
		if streamErr != nil {
			if *streamErr == "client went away" {
				return a.stopWithPartial(roundText.String())
			}
			if a.recoverRound(errors.New(*streamErr), roundText.Len() == 0 && len(calls) == 0) {
				continue
			}
			return roundFailure(errors.New(*streamErr))
		}
		if a.stopRequested() {
			return a.stopWithPartial(roundText.String())
		}
		// Empty stream fallback: one buffered completion. A round that
		// yields neither text nor tool calls is usually a gateway blip, so
		// retry before believing it; three strikes ends the turn. Ending
		// silent would fake a READY with nothing done (the "random stop"
		// that forces another nudge), so a turn with zero progress fails
		// loudly instead.
		if roundText.Len() == 0 && len(calls) == 0 {
			if a.emptyRounds >= 2 {
				if len(a.records) > 0 || a.reply.Len() > 0 {
					a.finish(phaseDone)
					return nil
				}
				return turnFailed{"model returned an empty response \u2014 try again"}
			}
			a.emptyRounds++
			resp, err := a.provider.CompleteTools(a.ctx, a.system, a.messages, a.toolDefs, a.session.Meta.SessionID)
			if err != nil {
				if a.stopRequested() {
					return a.stopWithPartial(roundText.String())
				}
				if a.recoverRound(err, true) {
					continue
				}
				return roundFailure(err)
			}
			roundText.WriteString(resp.Content)
			a.appendDelta(resp.Content)
			calls = resp.ToolCalls
			if resp.Usage != nil {
				a.noteCallUsage(*resp.Usage)
			}
			if resp.Content != "" {
				if err := a.emit(ChatChunk{Delta: resp.Content}); err != nil {
					return a.stopWithPartial(roundText.String())
				}
			}
			// Still empty after the buffered fallback: retry next round
			// instead of ending the turn silent (bounded by emptyRounds
			// above and maxRounds overall).
			if roundText.Len() == 0 && len(calls) == 0 {
				continue
			}
		}
		if len(calls) == 0 {
			if roundText.Len() > 0 {
				a.emptyRounds = 0
				a.retries, a.timeoutRetries = 0, 0
				a.records = append(a.records, transcript.Message{
					Role: transcript.RoleAssistant, Content: roundText.String(),
					CreatedAt: time.Now().UTC(),
				})
			}
			if a.shell != nil &&
				a.shell.PendingNotify(a.session.Meta.SessionID) &&
				!a.shell.WaitPendingNotify(a.session.Meta.SessionID, a.ctx) {
				if a.stopRequested() {
					return turnStop{}
				}
				return a.stopWithPartial(roundText.String())
			}
			drained := len(a.records)
			if err := a.drainNotifications(); err != nil {
				return err
			}
			if len(a.records) != drained {
				continue
			}
			if a.stopRequested() {
				return turnStop{}
			}
			a.finish(phaseDone)
			return nil
		}
		a.emptyRounds = 0
		a.retries, a.timeoutRetries = 0, 0
		text := roundText.String()
		a.appendAssistant(text, calls)
		if err := a.runToolsStreaming(calls); err != nil {
			return err
		}
	}
	a.finish(phaseNeedsContinue)
	return nil
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
		results[i] = a.tools.Call(&tools.ToolCall{ID: c.ID, Name: c.Name, Arguments: c.Arguments}, a.toolCtx)
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

// runPlain is the plain completion path for a provider with no tool defs.
func (a *Agent) runPlain() error {
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
