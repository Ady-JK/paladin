// Package agent owns the reusable agent template and one turn of one
// session. A Template is resolved against runtime dependencies to build
// an Agent; the Agent then runs the model/tool loop for a single turn.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"github.com/Ady-JK/paladin/core/internal/hooks"
	"github.com/Ady-JK/paladin/core/internal/inference"
	"github.com/Ady-JK/paladin/core/internal/permission"
	"github.com/Ady-JK/paladin/core/internal/redact"
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
	return &ApiError{Code: code, Message: redact.String(message)}
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
	// Steer, when set, returns (and clears) messages the user sent while
	// the turn runs; they join before the next model call. The turn hub
	// sets it; it never travels over the wire.
	Steer func() []SteerMessage `json:"-"`
	// Budget, when set, returns the session's spend limit in USD (0
	// means none). It is read before every round, so a limit changed
	// mid-turn applies at once. The runtime sets it; never on the wire.
	Budget func() float64 `json:"-"`
	// SpendLedgers, when set, lists the usage.json files whose spend
	// counts against Budget (a parent and its subagents share one
	// limit). Nil means the session's own ledger only.
	SpendLedgers func() []string `json:"-"`
	// Notices, when set, returns (and clears) reminders for the model,
	// such as a subagent's result; they join before the next model call.
	Notices func() []Notice `json:"-"`
}

// Notice is a reminder delivered into a running turn: recorded as a
// tool row (CallID, Name, Arguments) and shown to the model as Content.
type Notice struct {
	CallID    string
	Name      string
	Arguments string
	Content   string
	IsError   bool
}

// SteerMessage is a user message sent into a running turn.
type SteerMessage struct {
	Message string `json:"message"`
	// Display is the user's short form, as for Request.Display.
	Display string `json:"display,omitempty"`
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
	Delta     string        `json:"delta"`
	Done      bool          `json:"done"`
	Error     *ApiError     `json:"error,omitempty"`
	SessionID string        `json:"sessionId"`
	Stopped   bool          `json:"stopped"`
	Tool      *ToolActivity `json:"tool,omitempty"`
	Retry     *RetryStatus  `json:"retry,omitempty"`
	// Compaction reports context compaction progress.
	Compaction *CompactionStatus `json:"compaction,omitempty"`
	// Approval reports a permission prompt and its resolution.
	Approval *permission.Approval `json:"approval,omitempty"`
	// Steer echoes a mid-turn user message as it joins the conversation.
	Steer *SteerMessage `json:"steer,omitempty"`
	// Budget reports the session's spend limit being hit, or that it
	// cannot be enforced because the provider reports no cost.
	Budget *BudgetNotice `json:"budget,omitempty"`
	// Hook reports a hook that blocked, added context, failed or stopped.
	Hook *hooks.Note `json:"hook,omitempty"`
	// Reasoning is a piece of the model's thinking, streamed apart from
	// the answer (Delta).
	Reasoning string `json:"reasoning,omitempty"`
	// ThinkingDropped names a thinking level the provider rejected; the
	// call went on without it.
	ThinkingDropped string `json:"thinkingDropped,omitempty"`
}

// ToolGate decides whether a tool call may run, and under which
// sandbox. A nil gate runs everything unconfined.
type ToolGate interface {
	Authorize(ctx context.Context, callID, name string, args json.RawMessage, notify func(permission.Approval)) permission.Auth
	Escalate(ctx context.Context, callID, name string, args json.RawMessage, b permission.Blocked, notify func(permission.Approval)) permission.Escalation
}

// StepRecorder brackets each run of a file-changing tool, e.g. with
// workspace snapshots for undo. It must call run exactly once and return
// its result; recording is best-effort and must never fail the call.
type StepRecorder interface {
	Track(ctx context.Context, callID, name string, run func() tools.ToolResult) tools.ToolResult
}

// ChatOutcome is the summary returned when a turn finishes.
type ChatOutcome struct {
	SessionID string `json:"sessionId"`
	Reply     string `json:"reply"`
	Stopped   bool   `json:"stopped"`
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
)

type turnStop struct{}

func (turnStop) Error() string { return "stopped" }

type turnFailed struct{ msg string }

func (e turnFailed) Error() string { return e.msg }

// runConfig is everything a template resolves before the loop starts.
type runConfig struct {
	persistGate func(persist func() error)
	gate        ToolGate
	steps       StepRecorder
	steer       func() []SteerMessage
	budget      func() float64
	notices     func() []Notice
	ledgers     func() []string
	session     transcript.Session
	provider    inference.Provider
	tools       *tools.Registry
	shell       *tools.ShellTool
	toolDefs    []inference.ToolDefinition
	history     []transcript.Message
	system      string
	ctx         context.Context
	cancel      context.CancelFunc
	tx          chan<- ChatChunk
	done        <-chan struct{}
	message     string
	display     string
	resume      bool
	compactOnly bool
	// hooks runs the user's hook scripts; sessionStart is the
	// SessionStart source ("startup"/"resume") when this turn opens the
	// session, "" otherwise.
	hooks          *hooks.Engine
	sessionStart   string
	permissionMode string
	live           func([]inference.LlmMessage)
}

// Agent runs one turn. Built by Template.Build and dropped at exit;
// nothing survives between turns except the on-disk transcript.
type Agent struct {
	session  transcript.Session
	provider inference.Provider
	tools    *tools.Registry
	shell    *tools.ShellTool
	doom     doomGuard
	cancel   context.CancelFunc
	ctx      context.Context
	toolCtx  *tools.ToolCtx
	gate     ToolGate
	steps    StepRecorder
	// steer, when set, hands over messages the user sent mid-turn.
	steer  func() []SteerMessage
	budget func() float64
	// notices hands over reminders from outside the turn (subagents).
	notices func() []Notice
	// spendLedgers lists the ledgers the budget counts; nil is just ours.
	spendLedgers func() []string
	// budgetChecked: the turn's first budget check ran; budgetWrapUp:
	// the limit was hit and the final no-tools summary round is running;
	// costWarned: the unknown-cost warning went out; spendWarned: the
	// incomplete-spend warning went out.
	budgetChecked, budgetWrapUp, costWarned, spendWarned bool
	system                                               string
	history                                              []transcript.Message
	toolDefs                                             []inference.ToolDefinition
	phase                                                phase
	round                                                uint32
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
	persistGate func(persist func() error)
	// compactStreak counts compactions since a round last succeeded
	// with the context under the threshold.
	compactStreak int
	// promptTokens is the last reported prompt size, covering the first
	// promptLen entries of messages; sentLen is len(messages) at the
	// latest request.
	promptTokens int64
	promptLen    int
	sentLen      int
	messages     []inference.LlmMessage
	records      []transcript.Message
	reply        strings.Builder
	// thinking is the current round's visible reasoning, saved with
	// its assistant record.
	thinking strings.Builder
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

	hooks          *hooks.Engine
	sessionStart   string
	permissionMode string
	// live receives a copy of the conversation before each model round,
	// so side questions can see a turn that is still running.
	live func([]inference.LlmMessage)
	// stopContinuations counts Stop-hook blocks this turn; halt is a
	// hook's continue:false.
	stopContinuations int
	halt              hookHalt
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
		system:   c.system,
		history:  c.history,
		toolDefs: c.toolDefs,
		phase:    phaseCreated,
		tx:       c.tx,
		done:     c.done,

		persistGate: c.persistGate,
		gate:        c.gate,
		steps:       c.steps,
		steer:       c.steer,
		budget:      c.budget,
		notices:     c.notices,

		spendLedgers: c.ledgers,

		currentMessage: c.message,
		currentDisplay: c.display,
		currentResume:  c.resume,
		compactOnly:    c.compactOnly,

		hooks:          c.hooks,
		sessionStart:   c.sessionStart,
		live:           c.live,
		permissionMode: c.permissionMode,
	}
}

// Run executes the built agent's turn.
func (a *Agent) Run() (ChatOutcome, error) {
	sessionID := a.session.Meta.SessionID
	a.phase = phaseRunning
	a.emptyRounds = 0
	a.retries, a.timeoutRetries = 0, 0
	if !a.compactOnly && !a.currentResume {
		if err := a.runPromptHooks(); err != nil {
			return ChatOutcome{}, err
		}
	}
	a.messages = a.modelMessages(a.currentMessage, a.currentResume)
	if a.compactOnly {
		return a.runCompact()
	}

	err := a.safeLoop()
	a.settleNotices(nil)
	stopped := errors.As(err, new(turnStop))
	if err != nil && !stopped {
		_ = a.save()
		return ChatOutcome{}, err
	}
	a.finalize(stopped)
	if perr := a.save(); perr != nil {
		return ChatOutcome{}, perr
	}
	return ChatOutcome{
		SessionID: sessionID,
		Reply:     a.reply.String(),
		Stopped:   stopped,
		Usage:     a.turnUsage,
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
	if errors.As(err, new(turnFailed)) && a.stopRequested() {
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

func (a *Agent) finish(p phase) { a.phase = p }

func (a *Agent) finalize(stopped bool) {
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
	a.persistGate(func() error {
		err = a.persist(a.currentMessage, a.currentResume)
		return err
	})
	return err
}

// persist appends the whole turn (the user message unless resuming,
// the model and tool records, then the reply) as one transcript line,
// so a failed or interrupted write never leaves half a turn.
func (a *Agent) persist(message string, resume bool) error {
	turn := make([]transcript.Message, 0, len(a.records)+2)
	if !resume {
		msg := transcript.UserMessage(message)
		if d := strings.TrimSpace(a.currentDisplay); d != "" && d != strings.TrimSpace(message) {
			msg.Display = a.currentDisplay
		}
		turn = append(turn, msg)
	}
	turn = append(turn, a.records...)
	if len(a.toolDefs) == 0 && !a.compactOnly && (a.reply.Len() > 0 || resume) {
		turn = append(turn, transcript.Message{Role: transcript.RoleAssistant, Content: a.reply.String()})
	}
	if err := a.session.AppendTurn(turn); err != nil {
		return turnFailed{"persist turn: " + err.Error()}
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
	defer a.noteCallCost(u)
	if n := contextTokens(u, a.provider.ApiType()); n > 0 {
		a.promptTokens, a.promptLen = n, a.sentLen
	}
	session.RecordCall(a.session.UsagePath(), a.session.Meta.SessionID, a.provider.Model(), UsageCounters(u))
}

// UsageCounters is one LLM call's usage in ledger form.
func UsageCounters(u inference.Usage) session.Counters {
	return session.Counters{
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		ReasoningTokens:  u.ReasoningTokens,
		CacheReadTokens:  u.CacheReadTokens,
		CacheWriteTokens: u.CacheWriteTokens,
		TotalTokens:      u.TotalTokens,
		Cost:             u.Cost,
		ListCost:         u.ListCost,
		HasCost:          u.HasCost,
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

// noteThinking forwards a provider's thinking or dropped-level notice;
// thinking is kept for the round's assistant record.
func (a *Agent) noteThinking(c inference.Chunk) error {
	if c.EffortDropped != "" {
		if err := a.emit(ChatChunk{ThinkingDropped: c.EffortDropped}); err != nil {
			return err
		}
	}
	if c.Reasoning == "" {
		return nil
	}
	a.thinking.WriteString(c.Reasoning)
	return a.emit(ChatChunk{Reasoning: c.Reasoning})
}

// takeThinking hands over the current round's thinking for its record.
func (a *Agent) takeThinking() string {
	s := a.thinking.String()
	a.thinking.Reset()
	return s
}

// publishLive hands a copy of the conversation to the live observer
// (/btw): before each model round, and as tool calls start and finish.
func (a *Agent) publishLive() {
	if a.live != nil {
		a.live(append([]inference.LlmMessage(nil), a.messages...))
	}
}

func (a *Agent) appendAssistant(content string, calls []inference.ToolCall) {
	a.messages = append(a.messages, inference.LlmMessage{
		Role:      "assistant",
		Content:   content,
		ToolCalls: append([]inference.ToolCall(nil), calls...),
	})
	if len(calls) > 0 {
		a.publishLive()
	}
	stored := make([]transcript.ToolCall, 0, len(calls))
	for _, c := range calls {
		stored = append(stored, transcript.ToolCall{ID: c.ID, Name: c.Name, Arguments: c.Arguments, Signature: c.Signature})
	}
	a.records = append(a.records, transcript.Message{
		Role:      transcript.RoleAssistant,
		Content:   content,
		CreatedAt: time.Now().UTC(),
		ToolCalls: &stored,
		Reasoning: a.takeThinking(),
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
	a.publishLive()
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

func (a *Agent) appendNotify(content, callID, name string, isErr bool) {
	a.messages = append(a.messages, inference.LlmMessage{Role: "user", Content: content})
	a.records = append(a.records, transcript.Message{
		Role: transcript.RoleTool, Content: content,
		CreatedAt:  time.Now().UTC(),
		ToolCallID: strptr(callID), Name: strptr(name),
		IsError: isErr, IsNotify: true,
	})
}

func strptr(s string) *string { return &s }
