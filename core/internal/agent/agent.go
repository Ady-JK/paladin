// Package agent owns the reusable agent template and one turn of one
// session. A Template is resolved against runtime dependencies to build
// an Agent; the Agent then runs the model/tool loop for a single turn.
package agent

import (
	"context"
	"encoding/json"
	"os"
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

// Request is one turn request. The JSON shape is the wire contract with
// the Electron side and must stay stable.
type Request struct {
	SessionID    string  `json:"sessionId"`
	Message      string  `json:"message"`
	Cwd          string  `json:"cwd"`
	Provider     *string `json:"provider"`
	Model        *string `json:"model"`
	SystemPrompt *string `json:"systemPrompt"`
	Resume       bool    `json:"resume"`
}

// ToolActivity is one model-requested tool call as shown in the UI.
type ToolActivity struct {
	Arguments string `json:"arguments"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	Result    string `json:"result"`
	Status    string `json:"status"`
}

// ChatChunk is one streamed piece of a turn: text, a tool update, or the
// final done marker.
type ChatChunk struct {
	Delta         string        `json:"delta"`
	Done          bool          `json:"done"`
	Error         *ApiError     `json:"error,omitempty"`
	NeedsContinue bool          `json:"needsContinue"`
	SessionID     string        `json:"sessionId"`
	Stopped       bool          `json:"stopped"`
	Tool          *ToolActivity `json:"tool,omitempty"`
}

// ChatOutcome is the summary returned when a turn finishes.
type ChatOutcome struct {
	SessionID     string `json:"sessionId"`
	Reply         string `json:"reply"`
	Stopped       bool   `json:"stopped"`
	NeedsContinue bool   `json:"needsContinue"`
	// Usage is the turn's billed footprint: every LLM call's tokens
	// and gateway-reported spend folded together.
	Usage inference.Usage `json:"-"`
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

// runConfig is everything a template resolves before the loop starts.
type runConfig struct {
	session   transcript.Session
	provider  inference.Provider
	tools     *tools.Registry
	shell     *tools.ShellTool
	toolDefs  []inference.ToolDefinition
	history   []transcript.Message
	system    string
	maxRounds uint32
	ctx       context.Context
	cancel    context.CancelFunc
	tx        chan<- ChatChunk
	done      <-chan struct{}
	message   string
	resume    bool
}

// Agent runs one turn. Built by Template.Build and dropped at exit;
// nothing survives between turns except the on-disk transcript.
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
	// turnUsage accumulates every LLM call's Usage for the turn, across
	// all rounds and both streaming and fallback paths.
	turnUsage inference.Usage
	tx        chan<- ChatChunk
	done      <-chan struct{}

	// currentMessage and currentResume are the turn input, held so Run
	// can persist exactly what was asked.
	currentMessage string
	currentResume  bool
}

func newAgent(c runConfig) *Agent {
	return &Agent{
		session:  c.session,
		provider: c.provider,
		tools:    c.tools,
		shell:    c.shell,
		cancel:   c.cancel,
		ctx:      c.ctx,
		toolCtx: &tools.ToolCtx{
			Workspace:  c.session.Meta.Cwd,
			SessionDir: c.session.Dir,
			Cancel:     c.ctx,
		},
		system:    c.system,
		maxRounds: c.maxRounds,
		history:   c.history,
		toolDefs:  c.toolDefs,
		phase:     phaseCreated,
		tx:        c.tx,
		done:      c.done,

		currentMessage: c.message,
		currentResume:  c.resume,
	}
}

// Run executes the built agent's turn.
func (a *Agent) Run() (ChatOutcome, error) {
	sessionID := a.session.Meta.SessionID
	a.phase = phaseRunning
	a.messages = a.modelMessages(a.currentMessage, a.currentResume)

	err := a.runLoop()
	stopped := false
	switch e := err.(type) {
	case nil:
	case turnStop:
		stopped = true
	case turnFailed:
		_ = a.persist(a.currentMessage, a.currentResume)
		return ChatOutcome{}, e
	default:
		_ = a.persist(a.currentMessage, a.currentResume)
		return ChatOutcome{}, e
	}
	a.finalize(stopped)
	if perr := a.persist(a.currentMessage, a.currentResume); perr != nil {
		return ChatOutcome{}, perr
	}
	needsContinue := a.phase == phaseNeedsContinue
	return ChatOutcome{
		SessionID:     sessionID,
		Reply:         a.reply.String(),
		Stopped:       stopped,
		NeedsContinue: needsContinue,
		Usage:         a.turnUsage,
	}, nil
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
			return turnFailed{"persist user message: " + err.Error()}
		}
	}
	for _, record := range a.records {
		if err := a.session.AppendMessage(record); err != nil {
			return turnFailed{"persist turn: " + err.Error()}
		}
	}
	if len(a.toolDefs) == 0 && (a.reply.Len() > 0 || resume) {
		if err := a.session.Append(transcript.RoleAssistant, a.reply.String()); err != nil {
			return turnFailed{"persist reply: " + err.Error()}
		}
	}
	a.recordUsage()
	return nil
}

// recordUsage folds the turn's billed footprint into the session's
// usage.json ledger. It runs on every persist path (done, stopped and
// failed turns all spent tokens) and is best-effort: billing must never
// fail a turn, so errors are swallowed and a corrupt ledger restarts
// fresh rather than blocking.
func (a *Agent) recordUsage() {
	if a.turnUsage.Empty() {
		return
	}
	u := a.turnUsage
	var totals session.UsageTotals
	if data, err := os.ReadFile(a.session.UsagePath()); err == nil {
		_ = json.Unmarshal(data, &totals)
	}
	totals.SessionID = a.session.Meta.SessionID
	totals.AddCall(a.provider.Model(),
		u.PromptTokens, u.CompletionTokens, u.ReasoningTokens,
		u.CacheReadTokens, u.CacheWriteTokens, u.TotalTokens,
		u.Cost, u.ListCost, u.HasCost, time.Now().UTC().Unix())
	if data, err := json.MarshalIndent(totals, "", "  "); err == nil {
		_ = os.WriteFile(a.session.UsagePath(), data, 0o644)
	}
}

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
		stored = append(stored, transcript.ToolCall{ID: c.ID, Name: c.Name, Arguments: c.Arguments, Signature: c.Signature})
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

func strptr(s string) *string { return &s }
