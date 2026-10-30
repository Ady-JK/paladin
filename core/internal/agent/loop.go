// loop.go is the per-turn execution loop. It streams one
// model round, runs any requested tools, appends the results, and
// repeats until the model stops asking for tools or the round cap is hit.

package agent

import (
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"github.com/Ady-JK/paladin/core/internal/inference"
	"github.com/Ady-JK/paladin/core/internal/session"
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
	a.thinking.Reset()
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
			if err := a.noteThinking(chunk); err != nil {
				res.clientGone = true
				break loop
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
	if err := a.noteThinking(inference.Chunk{Reasoning: resp.Reasoning}); err != nil {
		return false, a.stopWithPartial(round.text)
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
			CreatedAt: time.Now().UTC(), Reasoning: a.takeThinking(),
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
			CreatedAt: time.Now().UTC(), Reasoning: a.takeThinking(),
		})
	}
	return turnStop{}
}

// maxParallelTools caps one concurrent batch.
const maxParallelTools = 8

// maxEscalations bounds sandbox-block prompts per call: each granted
// folder reruns the command, which may then hit the next folder.
const maxEscalations = 3

// NoticeInterruptedContent is the row result for a tool call the model
// announced but never finished sending (stop, failure or retry).
const NoticeInterruptedContent = `{"error":"interrupted: the model's tool call never completed, so it did not run"}`

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
			if err := a.noteThinking(chunk); err != nil {
				return turnStop{}
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
			if a.retryRound(err, true) {
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
