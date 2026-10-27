// Package agent: loop.go is the per-turn execution loop. It streams one
// model round, runs any requested tools, appends the results, and
// repeats until the model stops asking for tools or the round cap is hit.
package agent

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/Ady-JK/paladin/core/internal/inference"
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
		prx := make(chan inference.Chunk, 64)
		providerErr := make(chan error, 1)
		go func(msgs []inference.LlmMessage, defs []inference.ToolDefinition, sid string) {
			defer close(prx)
			providerErr <- a.provider.StreamTools(a.ctx, a.system, msgs, defs, sid, prx, a.done)
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
				if chunk.ToolNotice != nil {
					notice := chunk.ToolNotice
					name := notice.Name
					if name == "" {
						name = "tool"
					}
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
			return turnFailed{"provider unavailable: " + taskErr.Error()}
		}
		if streamErr != nil {
			if *streamErr == "client went away" {
				return a.stopWithPartial(roundText.String())
			}
			return turnFailed{"provider unavailable: " + *streamErr}
		}
		if a.stopRequested() {
			return a.stopWithPartial(roundText.String())
		}
		// Empty stream fallback: one buffered completion.
		if roundText.Len() == 0 && len(calls) == 0 {
			resp, err := a.provider.CompleteTools(a.ctx, a.system, a.messages, a.toolDefs, a.session.Meta.SessionID)
			if err != nil {
				if a.stopRequested() {
					return a.stopWithPartial(roundText.String())
				}
				return turnFailed{"provider unavailable: " + err.Error()}
			}
			roundText.WriteString(resp.Content)
			a.appendDelta(resp.Content)
			calls = resp.ToolCalls
			if resp.Content != "" {
				if err := a.emit(ChatChunk{Delta: resp.Content}); err != nil {
					return a.stopWithPartial(roundText.String())
				}
			}
		}
		if len(calls) == 0 {
			if roundText.Len() > 0 {
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
	for _, call := range calls {
		call := call
		if err := a.emit(ChatChunk{Tool: &ToolActivity{
			ID: call.ID, Name: call.Name, Arguments: call.Arguments,
			Status: session.ToolRunning,
		}}); err != nil {
			return turnStop{}
		}
	}
	for _, call := range calls {
		call := call
		if a.stopRequested() {
			return turnStop{}
		}
		result := a.tools.Call(&tools.ToolCall{ID: call.ID, Name: call.Name, Arguments: call.Arguments}, a.toolCtx)
		a.armNotify(&call, &result)
		status := session.ToolOK
		if result.IsError {
			status = session.ToolFailed
		}
		if err := a.emit(ChatChunk{Tool: &ToolActivity{
			ID: result.CallID, Name: result.Name, Arguments: call.Arguments,
			Result: result.Content, Status: status,
		}}); err != nil {
			return turnStop{}
		}
		inCall := inference.ToolCall{ID: call.ID, Name: call.Name, Arguments: call.Arguments}
		a.appendToolResult(&inCall, &result)
	}
	return nil
}

func (a *Agent) armNotify(call *inference.ToolCall, result *tools.ToolResult) {
	if a.shell == nil || call.Name != "shell" || result.IsError {
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
		providerErr <- a.provider.StreamTools(a.ctx, a.system, append([]inference.LlmMessage(nil), a.messages...), nil, a.session.Meta.SessionID, prx, a.done)
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
				return turnFailed{"provider unavailable: " + chunk.Err}
			}
			if chunk.Text != "" {
				streamed = true
				a.appendDelta(chunk.Text)
				if err := a.emit(ChatChunk{Delta: chunk.Text}); err != nil {
					return turnStop{}
				}
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
			return turnFailed{"provider unavailable: " + err.Error()}
		}
		a.appendDelta(resp.Content)
		content := a.reply.String()
		if err := a.emit(ChatChunk{Delta: content}); err != nil {
			return turnStop{}
		}
	}
	a.finish(phaseDone)
	return nil
}
