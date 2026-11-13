// loop.go is the per-turn execution loop. It streams one
// model round, runs any requested tools, appends the results, and
// repeats until the model finishes, the user stops it, or an error occurs.

package agent

import (
	"context"
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

type roundAction uint8

const (
	processRound roundAction = iota
	retryRound
	finishRound
)

func (a *Agent) runLoop() error {
	if len(a.toolDefs) == 0 {
		return a.runPlain()
	}
	// A productive turn has no fixed round cap.
	for {
		if end, err := a.prepareToolRound(); end {
			return err
		}
		round := a.streamRound()
		action, err := a.resolveToolRound(&round)
		switch action {
		case retryRound:
			continue
		case finishRound:
			return err
		}
		if len(round.calls) == 0 || a.budgetWrapUp {
			more, err := a.finishTextRound(round.text)
			if more {
				continue
			}
			return err
		}
		a.emptyRounds = 0
		a.retries, a.timeoutRetries = 0, 0
		// A tool round resets plan stalls and retry budgets.
		a.planNudges = 0
		canonicalizeCalls(a.tools, round.calls)
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

// prepareToolRound processes pending user input before contacting the model.
func (a *Agent) prepareToolRound() (bool, error) {
	a.round++
	if a.stopRequested() {
		return true, turnStop{}
	}
	if end, err := a.checkBudget(); end || err != nil {
		return true, err
	}
	if err := a.drainNotifications(); err != nil {
		return true, err
	}
	if err := a.drainSteering(); err != nil {
		return true, err
	}
	a.settleNotices(nil)
	a.maybeCompact()
	if a.stopRequested() {
		return true, turnStop{}
	}
	return false, nil
}

func (a *Agent) resolveToolRound(round *roundResult) (roundAction, error) {
	if a.stopRequested() {
		a.recordStoppedPartial(round.text)
		return finishRound, turnStop{}
	}
	if err := round.providerErr; err != nil {
		if inference.IsCancelled(err) || a.stopRequested() {
			a.recordStoppedPartial(round.text)
			return finishRound, turnStop{}
		}
		return a.resolveRoundError(*round, err)
	}
	if round.clientGone {
		a.recordStoppedPartial(round.text)
		return finishRound, turnStop{}
	}
	if round.streamErr != nil {
		return a.resolveRoundError(*round, round.streamErr)
	}
	a.noteRoundSucceeded()
	if round.empty() {
		return a.resolveEmptyToolRound(round)
	}
	return processRound, nil
}

func (a *Agent) resolveRoundError(round roundResult, err error) (roundAction, error) {
	if a.recoverRound(err, round.empty()) {
		return retryRound, nil
	}
	a.keepPartial(round)
	return finishRound, roundFailure(err)
}

// Empty model responses get a buffered fallback, then bounded retries.
func (a *Agent) resolveEmptyToolRound(round *roundResult) (roundAction, error) {
	if a.emptyRounds >= 2 {
		if len(a.records) > 0 || a.reply.Len() > 0 {
			a.finish(phaseDone)
			return finishRound, nil
		}
		return finishRound, turnFailed{"model returned an empty response — try again"}
	}
	a.emptyRounds++
	retry, err := a.completeBuffered(round)
	if err != nil {
		return finishRound, err
	}
	if retry || round.empty() {
		return retryRound, nil
	}
	return processRound, nil
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
	streamCtx, stopStream := context.WithCancel(a.ctx)
	defer stopStream()
	go a.startToolStream(streamCtx, prx, providerErr,
		append([]inference.LlmMessage(nil), a.messages...),
		append([]inference.ToolDefinition(nil), a.toolDefs...), a.session.Meta.SessionID)
	var stream toolRoundStream
	a.collectToolStream(prx, &stream, stopStream)
	stream.result.providerErr = <-providerErr
	if stream.checkpointFailed {
		stream.result.providerErr = nil
	}
	stream.result.text = stream.text.String()
	return stream.result
}

func (a *Agent) collectToolStream(chunks <-chan inference.Chunk, stream *toolRoundStream, stopStream context.CancelFunc) {
	for {
		select {
		case chunk, ok := <-chunks:
			if !ok {
				return
			}
			if a.consumeToolChunk(stream, chunk) {
				if stream.checkpointFailed {
					stopStream()
				}
				return
			}
			if a.stopRequested() {
				return
			}
		case <-a.ctx.Done():
			return
		case <-a.done:
			stream.result.clientGone = true
			return
		}
	}
}

func (a *Agent) startToolStream(ctx context.Context, chunks chan inference.Chunk, result chan<- error, messages []inference.LlmMessage, tools []inference.ToolDefinition, sessionID string) {
	defer close(chunks)
	result <- guardProvider(func() error {
		return a.TimeGen(func() error {
			return a.provider.StreamTools(ctx, a.system, messages, tools, sessionID, chunks, a.done)
		})
	})
}

type toolRoundStream struct {
	result           roundResult
	text             strings.Builder
	lastCheckpoint   time.Time
	checkpointFailed bool
}

// consumeToolChunk returns true when the stream must stop being consumed.
func (a *Agent) consumeToolChunk(stream *toolRoundStream, chunk inference.Chunk) bool {
	if chunk.Err != "" {
		stream.result.streamErr = errors.New(chunk.Err)
		return true
	}
	if chunk.Usage != nil {
		a.noteCallUsage(*chunk.Usage)
	}
	if err := a.noteThinking(chunk); err != nil {
		stream.result.clientGone = true
		return true
	}
	if chunk.ToolNotice != nil && a.emitToolNotice(chunk.ToolNotice) != nil {
		stream.result.clientGone = true
		return true
	}
	if chunk.Text != "" && a.consumeToolText(stream, chunk.Text) {
		return true
	}
	if len(chunk.ToolCalls) > 0 {
		stream.result.calls = chunk.ToolCalls
	}
	return false
}

func (a *Agent) emitToolNotice(notice *inference.ToolCall) error {
	name := notice.Name
	if name == "" {
		name = "tool"
	}
	a.noteNotice(notice.ID, name)
	return a.emit(ChatChunk{Tool: &ToolActivity{
		ID: notice.ID, Name: name, Arguments: "{}",
		Status: session.ToolRunning,
	}})
}

func (a *Agent) consumeToolText(stream *toolRoundStream, text string) bool {
	stream.text.WriteString(text)
	a.appendDelta(text)
	if time.Since(stream.lastCheckpoint) >= time.Second {
		if err := a.checkpoint(stream.text.String()); err != nil {
			stream.result.streamErr = err
			stream.checkpointFailed = true
			return true
		}
		stream.lastCheckpoint = time.Now()
	}
	if err := a.emit(ChatChunk{Delta: text}); err != nil {
		stream.result.clientGone = true
		return true
	}
	return false
}

// completeBuffered retries an empty streamed round as one buffered
// completion, filling round in. retry asks the loop to start the next
// round; err ends the turn.
func (a *Agent) completeBuffered(round *roundResult) (retry bool, err error) {
	resp, err := a.timeGenComplete(func() (inference.ToolResponse, error) {
		return a.provider.CompleteTools(a.ctx, a.system, a.messages, a.toolDefs, a.session.Meta.SessionID)
	})
	if err != nil {
		if a.stopRequested() {
			a.recordStoppedPartial(round.text)
			return false, turnStop{}
		}
		if a.recoverRound(err, true) {
			return true, nil
		}
		return false, roundFailure(err)
	}
	if err := a.noteThinking(inference.Chunk{Reasoning: resp.Reasoning}); err != nil {
		a.recordStoppedPartial(round.text)
		return false, turnStop{}
	}
	round.text += resp.Content
	a.appendDelta(resp.Content)
	round.calls = resp.ToolCalls
	if resp.Usage != nil {
		a.noteCallUsage(*resp.Usage)
	}
	if resp.Content != "" {
		if err := a.emit(ChatChunk{Delta: resp.Content}); err != nil {
			a.recordStoppedPartial(round.text)
			return false, turnStop{}
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
		if err := a.checkpoint(""); err != nil {
			return false, err
		}
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
	if a.planNudge() {
		return true, nil
	}
	if msg := a.planStalled(); msg != "" {
		return false, turnFailed{msg}
	}
	if a.stopHooks(text) {
		return true, nil
	}
	a.finish(phaseDone)
	return false, nil
}

func (a *Agent) recordStoppedPartial(roundText string) {
	if roundText != "" {
		a.records = append(a.records, transcript.Message{
			Role: transcript.RoleAssistant, Content: roundText,
			CreatedAt: time.Now().UTC(), Reasoning: a.takeThinking(),
		})
	}
}

// keepPartial records text a round streamed before the provider failed,
// so a dropped connection does not throw away a half-written answer.
// Turns with tools persist records (the reply builder is only saved when
// tools are off), so exactly one copy is kept.
func (a *Agent) keepPartial(round roundResult) {
	if round.text == "" || len(round.calls) > 0 || len(a.toolDefs) == 0 {
		return
	}
	a.records = append(a.records, transcript.Message{
		Role: transcript.RoleAssistant, Content: round.text,
		CreatedAt: time.Now().UTC(), Reasoning: a.takeThinking(),
	})
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
	var notices []Notice
	if a.shell != nil {
		for _, job := range a.shell.PollNotify(a.session.Meta.SessionID) {
			notices = append(notices, Notice{
				CallID: session.NotifyCallID(job.CallID), Name: "shell",
				Arguments: session.NotifyArgs(job.JobID),
				Content:   notifyContent(&job), IsError: job.IsError,
			})
		}
	}
	if a.notices != nil {
		notices = append(notices, a.notices()...)
	}
	for _, n := range notices {
		a.appendNotify(n.Content, n.CallID, n.Name, n.IsError)
		if err := a.checkpoint(""); err != nil {
			return err
		}
		status := session.ToolOK
		if n.IsError {
			status = session.ToolFailed
		}
		if err := a.emit(ChatChunk{Tool: &ToolActivity{
			ID: n.CallID, Name: n.Name, Arguments: n.Arguments,
			Result: n.Content, Status: status,
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
		if err := a.checkpoint(""); err != nil {
			return err
		}
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
	streamed, retry, err := a.streamPlainRound()
	if retry {
		return a.runPlain()
	}
	if err != nil {
		return err
	}
	if a.stopRequested() {
		return turnStop{}
	}
	if !streamed {
		retry, err = a.completePlainRound()
		if retry {
			return a.runPlain()
		}
		if err != nil {
			return err
		}
	}
	if a.reply.Len() == 0 {
		return turnFailed{"model returned an empty response — try again"}
	}
	a.finish(phaseDone)
	return nil
}

func (a *Agent) streamPlainRound() (streamed, retry bool, err error) {
	chunks := make(chan inference.Chunk, 64)
	providerErr := make(chan error, 1)
	streamCtx, stopStream := context.WithCancel(a.ctx)
	defer stopStream()
	go a.startToolStream(streamCtx, chunks, providerErr,
		append([]inference.LlmMessage(nil), a.messages...), nil, a.session.Meta.SessionID)
	streamed, retry, err = a.collectPlainStream(chunks)
	if retry || err != nil {
		return streamed, retry, err
	}
	if err := <-providerErr; err != nil {
		if inference.IsCancelled(err) || a.stopRequested() {
			return streamed, false, turnStop{}
		}
		if !streamed && a.retryRound(err, true) {
			return streamed, true, nil
		}
		return streamed, false, turnFailed{"provider unavailable: " + err.Error()}
	}
	return streamed, false, nil
}

type plainStream struct {
	streamed       bool
	lastCheckpoint time.Time
}

func (a *Agent) collectPlainStream(chunks <-chan inference.Chunk) (bool, bool, error) {
	var stream plainStream
	for {
		select {
		case chunk, ok := <-chunks:
			if !ok {
				return stream.streamed, false, nil
			}
			retry, err := a.consumePlainChunk(&stream, chunk)
			if retry || err != nil {
				return stream.streamed, retry, err
			}
		case <-a.ctx.Done():
			return stream.streamed, false, turnStop{}
		case <-a.done:
			return stream.streamed, false, turnStop{}
		}
	}
}

func (a *Agent) consumePlainChunk(stream *plainStream, chunk inference.Chunk) (bool, error) {
	if chunk.Err != "" {
		if a.stopRequested() {
			return false, turnStop{}
		}
		if !stream.streamed && a.retryRound(errors.New(chunk.Err), true) {
			return true, nil
		}
		return false, turnFailed{"provider unavailable: " + chunk.Err}
	}
	if err := a.noteThinking(chunk); err != nil {
		return false, turnStop{}
	}
	if chunk.Text != "" {
		if err := a.consumePlainText(stream, chunk.Text); err != nil {
			return false, err
		}
	}
	if chunk.Usage != nil {
		a.noteCallUsage(*chunk.Usage)
	}
	if a.stopRequested() {
		return false, turnStop{}
	}
	return false, nil
}

func (a *Agent) consumePlainText(stream *plainStream, text string) error {
	stream.streamed = true
	a.appendDelta(text)
	if time.Since(stream.lastCheckpoint) >= time.Second {
		if err := a.checkpoint(a.reply.String()); err != nil {
			return err
		}
		stream.lastCheckpoint = time.Now()
	}
	if err := a.emit(ChatChunk{Delta: text}); err != nil {
		return turnStop{}
	}
	return nil
}

func (a *Agent) completePlainRound() (bool, error) {
	resp, err := a.timeGenComplete(func() (inference.ToolResponse, error) {
		return a.provider.CompleteTools(a.ctx, a.system, a.messages, nil, a.session.Meta.SessionID)
	})
	if err != nil {
		if a.stopRequested() {
			return false, turnStop{}
		}
		if a.retryRound(err, true) {
			return true, nil
		}
		return false, turnFailed{"provider unavailable: " + err.Error()}
	}
	a.appendDelta(resp.Content)
	if resp.Usage != nil {
		a.noteCallUsage(*resp.Usage)
	}
	if err := a.emit(ChatChunk{Delta: a.reply.String()}); err != nil {
		return false, turnStop{}
	}
	return false, nil
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
