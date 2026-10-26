// Package turn owns one turn of one session. Built per chat() call and
// dropped at exit; nothing survives between turns except the on-disk
// transcript.
package turn

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/Ady-JK/paladin/core/internal/inference"
	"github.com/Ady-JK/paladin/core/internal/session"
	"github.com/Ady-JK/paladin/core/internal/tools"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

// ApiError is a coded turn error surfaced on the final done chunk.
type ApiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func NewApiError(code, message string) *ApiError {
	return &ApiError{Code: code, Message: message}
}

type ChatRequest struct {
	SessionID    string  `json:"sessionId"`
	Message      string  `json:"message"`
	Cwd          string  `json:"cwd"`
	Provider     *string `json:"provider"`
	Model        *string `json:"model"`
	SystemPrompt *string `json:"systemPrompt"`
	Resume       bool    `json:"resume"`
}

type ToolActivity struct {
	Arguments string `json:"arguments"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	Result    string `json:"result"`
	Status    string `json:"status"`
}

type ChatChunk struct {
	Delta         string        `json:"delta"`
	Done          bool          `json:"done"`
	Error         *ApiError     `json:"error,omitempty"`
	NeedsContinue bool          `json:"needsContinue"`
	SessionID     string        `json:"sessionId"`
	Stopped       bool          `json:"stopped"`
	Tool          *ToolActivity `json:"tool,omitempty"`
}

type ChatOutcome struct {
	SessionID     string `json:"sessionId"`
	Reply         string `json:"reply"`
	Stopped       bool   `json:"stopped"`
	NeedsContinue bool   `json:"needsContinue"`
}

type phase int

const (
	phaseCreated phase = iota
	phaseRunning
	phaseDone
	phaseStopped
	phaseNeedsContinue
)

type turnStop struct{}

func (turnStop) Error() string { return "stopped" }

type turnFailed struct{ msg string }

func (e turnFailed) Error() string { return e.msg }

// Agent runs one turn.
type Agent struct {
	session   transcript.Session
	provider  inference.Provider
	tools     *tools.Registry
	shell     *tools.ShellTool
	cancel    context.CancelFunc
	ctx       context.Context
	toolCtx   *tools.ToolCtx
	system    string
	maxRounds uint32
	history   []transcript.Message
	toolDefs  []inference.ToolDefinition
	phase     phase
	round     uint32
	messages  []inference.LlmMessage
	records   []transcript.Message
	reply     strings.Builder
	tx        chan<- ChatChunk
	done      <-chan struct{}
}

func NewAgent(sess transcript.Session, provider inference.Provider, registry *tools.Registry, shell *tools.ShellTool, ctx context.Context, cancel context.CancelFunc, system string, maxRounds uint32, history []transcript.Message, toolDefs []inference.ToolDefinition, tx chan<- ChatChunk, done <-chan struct{}) *Agent {
	return &Agent{
		session:  sess,
		provider: provider,
		tools:    registry,
		shell:    shell,
		cancel:   cancel,
		ctx:      ctx,
		toolCtx: &tools.ToolCtx{
			Workspace:  sess.Meta.Cwd,
			SessionDir: sess.Dir,
			Cancel:     ctx,
		},
		system:    system,
		maxRounds: maxRounds,
		history:   history,
		toolDefs:  toolDefs,
		phase:     phaseCreated,
		tx:        tx,
		done:      done,
	}
}

func strptr(s string) *string { return &s }

func (a *Agent) RunTurn(message string, resume bool) (ChatOutcome, error) {
	sessionID := a.session.Meta.SessionID
	if message == "" && !resume {
		return ChatOutcome{}, &turnFailed{"message is required"}
	}
	a.phase = phaseRunning
	a.messages = a.modelMessages(message, resume)

	err := a.runLoop()
	stopped := false
	switch e := err.(type) {
	case nil:
	case turnStop:
		stopped = true
	case turnFailed:
		_ = a.persist(message, resume)
		return ChatOutcome{}, e
	default:
		_ = a.persist(message, resume)
		return ChatOutcome{}, e
	}
	a.finalize(stopped)
	if perr := a.persist(message, resume); perr != nil {
		return ChatOutcome{}, &turnFailed{perr.Error()}
	}
	needsContinue := a.phase == phaseNeedsContinue
	return ChatOutcome{
		SessionID:     sessionID,
		Reply:         a.reply.String(),
		Stopped:       stopped,
		NeedsContinue: needsContinue,
	}, nil
}

func (a *Agent) modelMessages(message string, resume bool) []inference.LlmMessage {
	withTools := len(a.toolDefs) > 0
	messages := LlmMessages(a.history, withTools)
	if withTools && !resume {
		messages = append(messages, inference.LlmMessage{Role: "user", Content: message})
	}
	return messages
}

func (a *Agent) stopRequested() bool {
	select {
	case <-a.ctx.Done():
		return true
	default:
		return false
	}
}

func (a *Agent) needsContinue() bool { return a.round >= a.maxRounds }

func (a *Agent) finish(p phase) { a.phase = p }

func (a *Agent) finalize(stopped bool) {
	if a.phase == phaseNeedsContinue {
		return
	}
	if stopped {
		a.finish(phaseStopped)
	} else {
		a.finish(phaseDone)
	}
}

func (a *Agent) persist(message string, resume bool) error {
	if !resume {
		if err := a.session.Append(transcript.RoleUser, message); err != nil {
			return &persistError{"persist user message: " + err.Error()}
		}
	}
	for _, record := range a.records {
		if err := a.session.AppendMessage(record); err != nil {
			return &persistError{"persist turn: " + err.Error()}
		}
	}
	if len(a.toolDefs) == 0 && (a.reply.Len() > 0 || resume) {
		if err := a.session.Append(transcript.RoleAssistant, a.reply.String()); err != nil {
			return &persistError{"persist reply: " + err.Error()}
		}
	}
	return nil
}

type persistError struct{ msg string }

func (e *persistError) Error() string { return e.msg }

func (a *Agent) emit(chunk ChatChunk) error {
	chunk.SessionID = a.session.Meta.SessionID
	select {
	case a.tx <- chunk:
		return nil
	case <-a.done:
		return turnStop{}
	}
}

func (a *Agent) appendDelta(text string) { a.reply.WriteString(text) }

func (a *Agent) appendAssistant(content string, calls []inference.ToolCall) {
	a.messages = append(a.messages, inference.LlmMessage{
		Role:      "assistant",
		Content:   content,
		ToolCalls: append([]inference.ToolCall(nil), calls...),
	})
	stored := make([]transcript.ToolCall, 0, len(calls))
	for _, c := range calls {
		stored = append(stored, transcript.ToolCall{ID: c.ID, Name: c.Name, Arguments: c.Arguments})
	}
	a.records = append(a.records, transcript.Message{
		Role:      transcript.RoleAssistant,
		Content:   content,
		CreatedAt: time.Now().UTC(),
		ToolCalls: &stored,
	})
}

func (a *Agent) appendToolResult(call *inference.ToolCall, result *tools.ToolResult) {
	var content string
	if result != nil {
		content = result.Content
	}
	callID := call.ID
	name := call.Name
	a.messages = append(a.messages, inference.LlmMessage{
		Role: "tool", Content: content,
		ToolCallID: &callID, Name: &name,
	})
	resCallID, resName := "", ""
	isErr := false
	if result != nil {
		resCallID, resName, isErr = result.CallID, result.Name, result.IsError
	}
	a.records = append(a.records, transcript.Message{
		Role: transcript.RoleTool, Content: content,
		CreatedAt:  time.Now().UTC(),
		ToolCallID: strptr(resCallID), Name: strptr(resName),
		IsError: isErr,
	})
}

func (a *Agent) appendNotify(content, callID string, isErr bool) {
	a.messages = append(a.messages, inference.LlmMessage{Role: "user", Content: content})
	a.records = append(a.records, transcript.Message{
		Role: transcript.RoleTool, Content: content,
		CreatedAt:  time.Now().UTC(),
		ToolCallID: strptr(callID), Name: strptr("shell"),
		IsError: isErr, IsNotify: true,
	})
}

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
			if a.shell.PendingNotify(a.session.Meta.SessionID) &&
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
	if call.Name != "shell" || result.IsError {
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
					calls = append(calls, inference.ToolCall{ID: call.ID, Name: call.Name, Arguments: call.Arguments})
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
