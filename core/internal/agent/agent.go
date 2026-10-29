// Package agent owns the reusable agent template and one turn of one
// session. A Template is resolved against runtime dependencies to build
// an Agent; the Agent then runs the model/tool loop for a single turn.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"github.com/Ady-JK/paladin/core/internal/inference"
	"github.com/Ady-JK/paladin/core/internal/permission"
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
// the Electron side and must stay stable. Skill carries a skill name the
// composer attached via `/skill <name>`: the composer text stays short
// (`/skill <name> <text>`), and the runtime injects the SKILL.md
// content into the model-facing message before the LLM call.
type Request struct {
	SessionID    string  `json:"sessionId"`
	Message      string  `json:"message"`
	Cwd          string  `json:"cwd"`
	Provider     *string `json:"provider"`
	Model        *string `json:"model"`
	SystemPrompt *string `json:"systemPrompt"`
	Resume       bool    `json:"resume"`
	Skill        *string `json:"skill,omitempty"`
	// Command names a custom/builtin prompt command (`/review`); Message
	// then holds its arguments.
	Command *string `json:"command,omitempty"`
	// Mode is "build" (default) or "plan" (read-only tools).
	Mode string `json:"mode,omitempty"`
	// Compact runs a manual compaction instead of a model turn.
	Compact bool `json:"compact,omitempty"`
	// Display is the user's short form of the message for history.
	Display string `json:"display,omitempty"`
	// Permissions is the approval preset: "read-only", "auto"
	// (default) or "full-access".
	Permissions string `json:"permissions,omitempty"`
}

const (
	ModeBuild = "build"
	ModePlan  = "plan"
)

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
	Retry         *RetryStatus  `json:"retry,omitempty"`
	// Compaction reports context compaction progress.
	Compaction *CompactionStatus `json:"compaction,omitempty"`
	// Approval reports a permission prompt and its resolution.
	Approval *permission.Approval `json:"approval,omitempty"`
}

// ToolGate decides whether a tool call may run, and under which
// sandbox. A nil gate runs everything unconfined.
type ToolGate interface {
	Authorize(ctx context.Context, callID, name string, args json.RawMessage, notify func(permission.Approval)) permission.Auth
	Escalate(ctx context.Context, callID, name string, args json.RawMessage, notify func(permission.Approval)) bool
}

// StepRecorder brackets each run of a file-changing tool, e.g. with
// workspace snapshots for undo. It must call run exactly once and return
// its result; recording is best-effort and must never fail the call.
type StepRecorder interface {
	Track(callID, name string, run func() tools.ToolResult) tools.ToolResult
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
	persistGate func(persist func())
	gate        ToolGate
	steps       StepRecorder
	session     transcript.Session
	provider    inference.Provider
	tools       *tools.Registry
	shell       *tools.ShellTool
	toolDefs    []inference.ToolDefinition
	history     []transcript.Message
	system      string
	maxRounds   uint32
	ctx         context.Context
	cancel      context.CancelFunc
	tx          chan<- ChatChunk
	done        <-chan struct{}
	message     string
	display     string
	resume      bool
	compactOnly bool
}

// Agent runs one turn. Built by Template.Build and dropped at exit;
// nothing survives between turns except the on-disk transcript.
type Agent struct {
	session   transcript.Session
	provider  inference.Provider
	tools     *tools.Registry
	shell     *tools.ShellTool
	doom      doomGuard
	cancel    context.CancelFunc
	ctx       context.Context
	toolCtx   *tools.ToolCtx
	gate      ToolGate
	steps     StepRecorder
	system    string
	maxRounds uint32
	history   []transcript.Message
	toolDefs  []inference.ToolDefinition
	phase     phase
	round     uint32
	// emptyRounds counts consecutive rounds that produced neither text
	// nor tool calls; three in a row ends the turn (done with progress,
	// failed without) instead of spinning on a dead gateway.
	emptyRounds int
	// retries counts retried rounds after retryable provider errors
	// (rate limits, 5xx, transport blips); three per turn, then the
	// turn fails. Any round progress resets it alongside emptyRounds.
	retries        int
	timeoutRetries int
	// window is the model's context window in tokens; 0 when unknown,
	// which disables proactive compaction (overflow still compacts).
	window int64
	// noticed holds tool rows announced while the model was still
	// streaming the call; any that never execute are settled as failed
	// so the UI never keeps a spinner for a call that won't run.
	noticed []inference.ToolCall
	// persistGate, when set, wraps the transcript write (see save).
	persistGate func(persist func())
	// compactions counts compaction attempts this turn.
	compactions int
	// promptTokens is the last reported prompt size, covering the first
	// promptLen entries of messages; sentLen is len(messages) at the
	// latest request.
	promptTokens int64
	promptLen    int
	sentLen      int
	messages     []inference.LlmMessage
	records      []transcript.Message
	reply        strings.Builder
	// turnUsage accumulates every LLM call's Usage for the turn, across
	// all rounds and both streaming and fallback paths.
	turnUsage inference.Usage
	tx        chan<- ChatChunk
	done      <-chan struct{}

	// currentMessage and currentResume are the turn input, held so Run
	// can persist exactly what was asked.
	currentMessage string
	currentDisplay string
	currentResume  bool
	// compactOnly runs a manual /compact instead of a model turn.
	compactOnly bool
}

func newAgent(c runConfig) *Agent {
	var window int64
	if c.provider != nil {
		if w, known := inference.ContextWindow(c.provider.Model()); known {
			window = w
		}
	}
	return &Agent{
		window:   window,
		session:  c.session,
		provider: c.provider,
		tools:    c.tools,
		shell:    c.shell,
		cancel:   c.cancel,
		ctx:      c.ctx,
		toolCtx: &tools.ToolCtx{
			Workspace:  c.session.Meta.Cwd,
			Project:    c.session.Meta.ProjectCwd(),
			SessionDir: c.session.Dir,
			Cancel:     c.ctx,
			SessionID:  c.session.Meta.SessionID,
		},
		system:    c.system,
		maxRounds: c.maxRounds,
		history:   c.history,
		toolDefs:  c.toolDefs,
		phase:     phaseCreated,
		tx:        c.tx,
		done:      c.done,

		persistGate: c.persistGate,
		gate:        c.gate,
		steps:       c.steps,

		currentMessage: c.message,
		currentDisplay: c.display,
		currentResume:  c.resume,
		compactOnly:    c.compactOnly,
	}
}

// Run executes the built agent's turn.
func (a *Agent) Run() (ChatOutcome, error) {
	sessionID := a.session.Meta.SessionID
	a.phase = phaseRunning
	a.emptyRounds = 0
	a.retries, a.timeoutRetries = 0, 0
	a.messages = a.modelMessages(a.currentMessage, a.currentResume)
	if a.compactOnly {
		return a.runCompact()
	}

	err := a.safeLoop()
	a.settleNotices(nil)
	stopped := false
	switch e := err.(type) {
	case nil:
	case turnStop:
		stopped = true
	case turnFailed:
		_ = a.save()
		return ChatOutcome{}, e
	default:
		_ = a.save()
		return ChatOutcome{}, e
	}
	a.finalize(stopped)
	if perr := a.save(); perr != nil {
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

// safeLoop turns a panic anywhere in the loop into a failed turn; the
// records gathered so far are still persisted by Run.
func (a *Agent) safeLoop() (err error) {
	defer func() {
		if p := recover(); p != nil {
			fmt.Fprintf(os.Stderr, "agent loop panicked: %v\n%s", p, debug.Stack())
			err = turnFailed{msg: fmt.Sprintf("internal error: %v", p)}
		}
	}()
	err = a.runLoop()
	// A failure observed after the user pressed stop (e.g. a backoff
	// cut short) is a stop, not an error.
	if _, failed := err.(turnFailed); failed && a.stopRequested() {
		return turnStop{}
	}
	return err
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

// save persists the turn, inside persistGate when one is set so the
// turn hub can order "now in the transcript" against attach snapshots.
func (a *Agent) save() error {
	if a.persistGate == nil {
		return a.persist(a.currentMessage, a.currentResume)
	}
	var err error
	a.persistGate(func() { err = a.persist(a.currentMessage, a.currentResume) })
	return err
}

func (a *Agent) persist(message string, resume bool) error {
	if !resume {
		msg := transcript.UserMessage(message)
		if d := strings.TrimSpace(a.currentDisplay); d != "" && d != strings.TrimSpace(message) {
			msg.Display = a.currentDisplay
		}
		if err := a.session.AppendMessage(msg); err != nil {
			return turnFailed{"persist user message: " + err.Error()}
		}
	}
	for _, record := range a.records {
		if err := a.session.AppendMessage(record); err != nil {
			return turnFailed{"persist turn: " + err.Error()}
		}
	}
	if len(a.toolDefs) == 0 && !a.compactOnly && (a.reply.Len() > 0 || resume) {
		if err := a.session.Append(transcript.RoleAssistant, a.reply.String()); err != nil {
			return turnFailed{"persist reply: " + err.Error()}
		}
	}
	return nil
}

// noteCallUsage folds one LLM call's usage into the turn totals and
// persists it to the session's usage.json immediately, so the context
// reading (last call's input over the model window) is current after
// every round, not just at turn end. It runs on every usage report —
// done, stopped and failed turns all spent tokens — and is best-effort:
// billing must never fail a turn, so errors are swallowed and a corrupt
// ledger restarts fresh rather than blocking.
func (a *Agent) noteCallUsage(u inference.Usage) {
	a.turnUsage.Add(u)
	if n := contextTokens(u, a.provider.ApiType()); n > 0 {
		a.promptTokens, a.promptLen = n, a.sentLen
	}
	var totals session.UsageTotals
	if data, err := os.ReadFile(a.session.UsagePath()); err == nil {
		_ = json.Unmarshal(data, &totals)
	}
	totals.SessionID = a.session.Meta.SessionID
	totals.AddCall(a.provider.Model(), session.Counters{
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		ReasoningTokens:  u.ReasoningTokens,
		CacheReadTokens:  u.CacheReadTokens,
		CacheWriteTokens: u.CacheWriteTokens,
		TotalTokens:      u.TotalTokens,
		Cost:             u.Cost,
		ListCost:         u.ListCost,
		HasCost:          u.HasCost,
	}, time.Now().UTC().Unix())
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
