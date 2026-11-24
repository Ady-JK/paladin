// The Codex harness: a session whose turns run on the OpenAI Codex CLI
// (`codex app-server`) instead of Paladin's own agent loop. Codex keeps its
// own login, models, sandbox and tools; Paladin supplies the UI, the
// transcript, the approval cards and the per-turn undo snapshots.

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Ady-JK/paladin/core/internal/agent"
	"github.com/Ady-JK/paladin/core/internal/codex"
	"github.com/Ady-JK/paladin/core/internal/harness/codexadapter"
	"github.com/Ady-JK/paladin/core/internal/permission"
	"github.com/Ady-JK/paladin/core/internal/session"
	"github.com/Ady-JK/paladin/core/internal/transcript"
	"github.com/Ady-JK/paladin/core/internal/worktree"
)

// HarnessCodex names the Codex harness (a session's Meta.Harness.Name and
// the provider name its UI model picker uses).
const HarnessCodex = "codex"

const (
	codexStartTimeout     = 45 * time.Second
	codexInterruptWait    = 8 * time.Second
	codexSteerPoll        = 250 * time.Millisecond
	codexPriorContextMax  = 24000
	codexToolOutputMax    = 64 * 1024
	codexApprovalTitleCap = 400
)

// CodexStatus reports whether Codex can be used. With deep false it only
// checks that the CLI is installed (no process starts); deep also signs in
// to the app-server and lists models.
func (r *AgentRuntime) CodexStatus(deep bool) codex.Status {
	if !deep {
		if _, err := codex.FindBinary(); err != nil {
			return codex.Status{Models: []codex.Model{}, Permissions: []string{}, Error: err.Error()}
		}
		return codex.Status{Installed: true, Models: []codex.Model{}, Permissions: []string{}}
	}
	ctx, cancel := context.WithTimeout(context.Background(), codexStartTimeout)
	defer cancel()
	return r.codex.Status(ctx)
}

func (r *AgentRuntime) codexModels() []codex.Model {
	ctx, cancel := context.WithTimeout(context.Background(), codexStartTimeout)
	defer cancel()
	models, err := r.codex.Models(ctx)
	if err != nil {
		return nil
	}
	return models
}

// codexThinkingGet is thinkingGet for the Codex harness: the levels are the
// model's reasoning efforts.
func (r *AgentRuntime) codexThinkingGet(model string) ThinkingView {
	v := ThinkingView{Provider: HarnessCodex, Model: strings.TrimSpace(model), Levels: []string{}}
	for _, m := range r.codexModels() {
		if m.ID == v.Model || (v.Model == "" && m.IsDefault) {
			v.Model = m.ID
			v.Levels = append(v.Levels, m.Efforts...)
			break
		}
	}
	v.Level = r.loadSettings().ThinkingLevels[HarnessCodex][v.Model]
	if !containsString(v.Levels, v.Level) {
		v.Level = ""
	}
	return v
}

func containsString(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}

// openHarnessSession creates a session that Codex will run. model may be
// empty (Codex picks its default).
func (r *AgentRuntime) openHarnessSession(cwd string, opts OpenOpts) (Session, error) {
	if err := findHarnessBinary(opts.Harness); err != nil {
		return Session{}, err
	}
	model := strings.TrimSpace(opts.Model)
	create := func(dir string) (transcript.Session, error) {
		sess, err := r.transcripts.Create(dir, opts.Harness, model)
		if err != nil {
			return sess, err
		}
		sess.Meta.Harness = &transcript.Harness{Name: opts.Harness}
		return sess, nil
	}
	if !opts.Worktree {
		sess, err := create(cwd)
		if err == nil {
			err = sess.SaveMeta()
		}
		if err != nil {
			return Session{}, err
		}
		return harnessSessionView(sess), nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	info, wcwd, err := r.worktrees.Create(ctx, cwd, opts.Name)
	if err != nil {
		return Session{}, err
	}
	sess, err := create(wcwd)
	if err == nil {
		sess.Meta.Worktree = toTranscriptWorktree(info, filepath.Clean(cwd))
		err = sess.SaveMeta()
	}
	if err != nil {
		_ = worktree.Remove(ctx, info, true)
		return Session{}, err
	}
	return harnessSessionView(sess), nil
}

func harnessSessionView(sess transcript.Session) Session {
	name := sess.Meta.Harness.Name
	return Session{SessionID: sess.Meta.SessionID, Cwd: sess.Meta.Cwd, Provider: name,
		Model: derefString(sess.Meta.Model), Harness: name, Worktree: session.WorktreeView(sess.Meta.Worktree)}
}

// codexPreset is the permission preset a turn asks for. Plan mode is Codex's
// own collaboration mode and does not change it.
func codexPreset(permission.Preset) string { return codex.PresetFullAccess }

// codexTool is one Codex item shown as a tool card.
type codexTool struct {
	id, name, args string
	result         string // set for a card built from a finished item
	output         strings.Builder
}

// codexRun is the state of one Codex turn while it streams.
type codexRun struct {
	r         *AgentRuntime
	client    *codex.Client
	sessionID string
	usagePath string
	dir       string
	planRow   int
	preset    string // the permission preset this turn runs under
	model     string
	thread    *codexThread
	threadID  string
	turnID    string
	tx        chan<- agent.ChatChunk
	ctx       context.Context

	records   []transcript.Message
	text      strings.Builder
	reasoning strings.Builder
	reply     strings.Builder
	streamed  map[string]int
	lastText  bool
	summary   map[string]int64
	tools     map[string]*codexTool
	questions map[string]bool // async question cards this turn asked
	approvals map[string]int
	procMu    sync.Mutex
	procs     map[string]string // running command item -> its process id
	outTokens int64

	status  string
	failure string
	// manualCompact distinguishes /compact from Codex's automatic compaction.
	manualCompact bool
}

func (c *codexRun) send(chunk agent.ChatChunk) {
	chunk.SessionID = c.sessionID
	c.tx <- chunk
}

// flushText turns the text and thinking gathered so far into one assistant
// record, carrying calls when a tool follows it.
func (c *codexRun) flushText(calls []transcript.ToolCall) {
	text, reasoning := c.text.String(), strings.TrimSpace(c.reasoning.String())
	c.text.Reset()
	c.reasoning.Reset()
	if strings.TrimSpace(text) == "" && reasoning == "" && len(calls) == 0 {
		return
	}
	msg := transcript.Message{Role: transcript.RoleAssistant, Content: text, Reasoning: reasoning, CreatedAt: time.Now().UTC()}
	if len(calls) > 0 {
		msg.ToolCalls = &calls
	}
	c.records = append(c.records, msg)
	c.lastText = false
}

// runHarnessTurn runs a turn on the session's external agent.
func (r *AgentRuntime) runHarnessTurn(ctx context.Context, sess transcript.Session, req agent.Request, workspace string, gate func(func() error), tx chan<- agent.ChatChunk) (agent.ChatOutcome, error) {
	switch sess.Meta.Harness.Name {
	case HarnessCodex:
		return r.runCodexTurn(ctx, sess, req, workspace, gate, tx)
	case HarnessClaude:
		return r.runClaudeTurn(ctx, sess, req, workspace, gate, tx)
	}
	return agent.ChatOutcome{}, fmt.Errorf("unknown harness %q", sess.Meta.Harness.Name)
}

// runCodexTurn runs one turn of a Codex session and persists it like the
// agent loop does: the user row, the assistant and tool rows, the stats. A
// resume turn adopts a turn Codex started by itself (a goal continuing): it
// has no user row.
func (r *AgentRuntime) runCodexTurn(ctx context.Context, sess transcript.Session, req agent.Request, workspace string, gate func(func() error), tx chan<- agent.ChatChunk) (agent.ChatOutcome, error) {
	if req.Command != nil && strings.TrimSpace(*req.Command) != "" || req.Skill != nil && strings.TrimSpace(*req.Skill) != "" {
		return agent.ChatOutcome{}, errors.New("prompt commands and skills are not available in a codex session")
	}
	preset, err := permission.ParsePreset(req.Permissions)
	if err != nil {
		return agent.ChatOutcome{}, err
	}
	adopt := req.Resume
	var cmd codexCommand
	switch {
	case adopt:
	case req.Compact:
		cmd = codexCommand{name: "compact"}
	default:
		cmd, _ = parseCodexCommand(req.Message)
	}
	startedAt := time.Now()
	model := strings.TrimSpace(derefString(req.Model))
	if p := derefString(req.Provider); p != HarnessCodex {
		model = ""
	}
	if model == "" {
		model = derefString(sess.Meta.Model)
	}
	history, _ := sess.History()
	index := len(history)

	var userRows []transcript.Message
	if !adopt {
		message := req.Message
		if req.Compact && message == "" {
			message = "/compact"
		}
		user := transcript.UserMessage(message)
		if d := strings.TrimSpace(req.Display); d != "" && d != strings.TrimSpace(message) {
			user.Display = req.Display
		}
		user.Attachments = req.Attachments
		userRows = []transcript.Message{user}
	}
	journal, jerr := sess.BeginJournal(userRows)
	if jerr != nil {
		return agent.ChatOutcome{}, fmt.Errorf("start turn journal: %w", jerr)
	}
	defer journal.Release()

	run := &codexRun{r: r, sessionID: req.SessionID, usagePath: sess.UsagePath(), dir: sess.Dir, planRow: -1, model: model, tx: tx, ctx: ctx,
		manualCompact: cmd.name == "compact",
		streamed:      map[string]int{}, summary: map[string]int64{}, tools: map[string]*codexTool{}, approvals: map[string]int{}}
	runErr := r.driveCodexTurn(ctx, run, &sess, req, cmd, model, workspace, preset, history)
	// The turn used some of the plan, so the usage view should show it.
	r.refreshCodexLimits(run.client)

	// A turn that ends early still lands whatever it produced.
	run.closeOpenTools()
	run.flushText(nil)
	turn := append(append([]transcript.Message{}, userRows...), run.records...)
	if run.turnID != "" && len(turn) > 0 {
		turn[0].HarnessTurn = run.turnID
	}
	stats := &transcript.TurnStats{DurationMs: time.Since(startedAt).Milliseconds(), Tokens: run.outTokens, Incomplete: runErr != nil || run.status == "interrupted"}
	var persistErr error
	gate(func() error {
		if err := sess.AppendJournalTurn(turn, stats, journal.ID); err != nil {
			persistErr = fmt.Errorf("persist turn: %w", err)
			return persistErr
		}
		if err := journal.Finish(); err != nil {
			persistErr = fmt.Errorf("clear turn journal: %w", err)
			return persistErr
		}
		return nil
	})
	if persistErr != nil {
		return agent.ChatOutcome{}, persistErr
	}
	out := agent.ChatOutcome{SessionID: req.SessionID, Reply: run.reply.String(), Stats: stats}
	if !adopt {
		out.Index = &index
	}
	if runErr != nil {
		if errors.Is(runErr, context.Canceled) || ctx.Err() != nil {
			out.Stopped = true
			r.emitDone(tx, req.SessionID, true, nil)
			return out, nil
		}
		r.emitDone(tx, req.SessionID, false, agent.NewApiError("internal", runErr.Error()))
		return out, runErr
	}
	r.emitDone(tx, req.SessionID, run.status == "interrupted", nil)
	out.Stopped = run.status == "interrupted"
	return out, nil
}

// driveCodexTurn gets the session's thread, starts what the turn asks for
// (a message, a compaction, a review, or nothing for an adopted turn) and
// streams the turn to its end.
func (r *AgentRuntime) driveCodexTurn(ctx context.Context, run *codexRun, sess *transcript.Session, req agent.Request, cmd codexCommand, model, workspace string, preset permission.Preset, history []transcript.Message) error {
	cctx, cancel := context.WithTimeout(ctx, codexStartTimeout)
	defer cancel()
	client, err := r.codex.Client(cctx)
	if err != nil {
		return err
	}
	run.client = client
	for _, n := range client.TakeNotices() {
		run.notice("warning", n)
	}
	// Codex refuses settings its requirements forbid (an administrator can
	// rule out full access, for one), so ask for the closest it accepts.
	choice, ok := client.Requirements().Choose(codexPreset(preset))
	if !ok {
		return errors.New("codex's managed settings allow none of the permission modes Paladin uses")
	}
	threadParams := codex.ThreadParams{Cwd: workspace, Model: model, ApprovalPolicy: choice.Approval, Sandbox: choice.Sandbox}
	th, fresh, err := r.codexThreadFor(cctx, sess, workspace, threadParams)
	if err != nil {
		return err
	}
	run.thread, run.threadID = th, th.threadID
	run.preset = codexPreset(preset)
	if run.model == "" {
		run.model = th.model
	}
	if model != "" && model != derefString(sess.Meta.Model) {
		_ = r.transcripts.UpdateMeta(req.Cwd, req.SessionID, func(m *transcript.Meta) error {
			m.Model = &model
			return nil
		})
	}
	if err := th.acquire(); err != nil {
		return err
	}
	defer th.release()
	th.discardIdle()

	switch {
	case req.Resume:
		// Codex already started this turn; its first messages are queued.
	case cmd.name == "compact":
		if err := client.CompactThread(cctx, th.threadID); err != nil {
			return err
		}
	case cmd.name == "review":
		if err := client.StartReview(cctx, th.threadID, cmd.reviewTarget()); err != nil {
			return err
		}
	case cmd.local():
		return run.localCommand(cctx, cmd)
	default:
		message := req.Message
		if fresh {
			// A thread started for a session that already has a
			// conversation (a fork) is told what was said so far.
			if prior := priorContext(history); prior != "" {
				message = prior + "\n\n" + message
			}
		}
		effort := r.loadSettings().ThinkingLevels[HarnessCodex][model]
		skills := mentionedSkills(cctx, client, workspace, req.Message)
		turnParams := codex.TurnParams{ThreadID: th.threadID, Input: codexInput(message, req, skills), Cwd: workspace, Model: model,
			Effort: effort, ApprovalPolicy: choice.Approval, SandboxPolicy: choice.SandboxPolicy(),
			Collaboration: codexCollaboration(req.Mode, model, effort), Summary: client.ReasoningSummary(cctx, workspace),
			ServiceTier: r.loadSettings().ServiceTiers[HarnessCodex][model]}
		if run.turnID, err = client.StartTurn(cctx, turnParams); err != nil {
			return err
		}
	}
	return run.stream(th.sub, req)
}

// codexCollaboration is Codex's collaboration mode for a turn: plan mode when
// the composer is in it, the default otherwise.
func codexCollaboration(mode, model, effort string) map[string]any {
	if model == "" {
		return nil
	}
	settings := map[string]any{"model": model}
	if effort != "" {
		settings["reasoning_effort"] = effort
	}
	kind := "default"
	if mode == "plan" {
		kind = "plan"
	}
	return map[string]any{"mode": kind, "settings": settings}
}

// codexInput is the turn's user input: the message, the skills it names (Codex
// also reads the $name in the text) and any images.
func codexInput(message string, req agent.Request, skills []codex.Skill) []map[string]any {
	input := []map[string]any{{"type": "text", "text": message}}
	for _, s := range skills {
		input = append(input, map[string]any{"type": "skill", "name": s.Name, "path": s.Path})
	}
	for _, a := range req.Attachments {
		if a.Image != nil && a.Path != "" {
			input = append(input, map[string]any{"type": "localImage", "path": a.Path})
		}
	}
	return input
}

// priorContext renders earlier conversation text for a fresh thread.
func priorContext(history []transcript.Message) string {
	var lines []string
	for _, m := range history {
		if m.IsCompaction() || m.Content == "" {
			continue
		}
		switch m.Role {
		case transcript.RoleUser:
			text := m.Content
			if m.Display != "" {
				text = m.Display
			}
			lines = append(lines, "User: "+text)
		case transcript.RoleAssistant:
			lines = append(lines, "Assistant: "+m.Content)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	text := strings.Join(lines, "\n\n")
	if len(text) > codexPriorContextMax {
		text = "…" + text[len(text)-codexPriorContextMax:]
	}
	return "The conversation so far, from before this session moved to a new Codex thread. Continue from it.\n\n" + text + "\n\n---\nNew message:"
}

// stream reads the thread's messages until the turn ends.
func (c *codexRun) stream(sub *codex.Subscription, req agent.Request) error {
	ctxDone := c.ctx.Done()
	var deadline <-chan time.Time
	tick := time.NewTicker(codexSteerPoll)
	defer tick.Stop()
	for {
		m, ok, closed := sub.Next()
		if !ok {
			if closed {
				err := c.client.Err()
				if err == nil {
					err = codex.ErrClosed
				}
				return err
			}
			select {
			case <-sub.Wake():
			case <-tick.C:
				c.steer(req)
			case <-ctxDone:
				ctxDone = nil
				deadline = time.After(codexInterruptWait)
				go c.interrupt()
			case <-deadline:
				return context.Canceled
			}
			continue
		}
		if done, err := c.handle(m); err != nil || done {
			return err
		}
	}
}

func (c *codexRun) interrupt() {
	ctx, cancel := context.WithTimeout(context.Background(), codexInterruptWait)
	defer cancel()
	// The commands Codex still has running are stopped alongside the turn, or a
	// build or a dev server would carry on after Stop.
	c.procMu.Lock()
	running := make([]string, 0, len(c.procs))
	for _, pid := range c.procs {
		running = append(running, pid)
	}
	c.procMu.Unlock()
	var wg sync.WaitGroup
	for _, pid := range running {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = c.client.TerminateProcess(ctx, c.threadID, pid)
		}()
	}
	_ = c.client.InterruptTurn(ctx, c.threadID, c.turnID)
	wg.Wait()
}

// steer hands messages the user sent mid-turn to Codex.
func (c *codexRun) steer(req agent.Request) {
	if req.Steer == nil || c.ctx.Err() != nil {
		return
	}
	for _, msg := range req.Steer() {
		if strings.TrimSpace(msg.Message) == "" {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), codexInterruptWait)
		err := c.client.SteerTurn(ctx, c.threadID, c.turnID, []map[string]any{{"type": "text", "text": msg.Message}})
		cancel()
		if err != nil {
			// Codex refused it (the turn is ending, say): hand it back to the
			// turn's queue so it is retried, or becomes the next turn if this
			// one ends first, rather than vanishing.
			if _, herr := c.r.hub.Steer(c.sessionID, msg.Message, msg.Display); herr != nil {
				log.Printf("codex: steer %s: %v (requeue: %v)", c.sessionID, err, herr)
			}
			continue
		}
		c.flushText(nil)
		c.settleQuestions(msg.Message)
		rec := transcript.UserMessage(msg.Message)
		if d := strings.TrimSpace(msg.Display); d != "" && d != strings.TrimSpace(msg.Message) {
			rec.Display = msg.Display
		}
		c.records = append(c.records, rec)
		steered := msg
		c.send(agent.ChatChunk{Steer: &steered})
	}
}

// handle applies one message; done is true when the turn is over.
func (c *codexRun) handle(m codex.Inbound) (done bool, err error) {
	p := codex.Decode(m.Params)
	if m.IsRequest() {
		return false, c.request(m, p)
	}
	// Messages about another turn (late ones from the one before) are not ours,
	// except that a background command of an earlier turn settles whenever it ends.
	if id := p.Str("turnId"); id != "" && c.turnID != "" && id != c.turnID {
		c.thread.noteLate(m.Method, p)
		return false, nil
	}
	switch m.Method {
	case "turn/started":
		if c.turnID == "" {
			c.turnID = p.Obj("turn").Str("id")
		}
	case "item/agentMessage/delta":
		c.agentDelta(p.Str("itemId"), p.Str("delta"))
	case "item/reasoning/summaryTextDelta", "item/reasoning/textDelta":
		key := p.Str("itemId")
		delta := p.Str("delta")
		if idx := p.Int("summaryIndex"); idx != c.summary[key] && c.reasoning.Len() > 0 {
			delta = "\n\n" + delta
			c.summary[key] = idx
		}
		c.reasoning.WriteString(delta)
		c.send(agent.ChatChunk{Reasoning: delta})
	case "item/started":
		c.itemStarted(p.Obj("item"))
	case "item/completed":
		c.itemCompleted(p.Obj("item"))
	case "item/commandExecution/outputDelta":
		if t := c.tools[p.Str("itemId")]; t != nil && t.output.Len() < codexToolOutputMax {
			t.output.WriteString(p.Str("delta"))
		}
	case "item/plan/delta":
		c.agentDelta(p.Str("itemId"), p.Str("delta"))
	case "turn/plan/updated":
		c.planUpdated(p)
	case "thread/tokenUsage/updated":
		c.usage(p.Obj("tokenUsage"))
	case "warning", "guardianWarning":
		c.notice("warning", p.Str("message"))
	case "configWarning", "deprecationNotice":
		c.notice("warning", strings.TrimSpace(p.Str("summary")+": "+p.Str("details")))
	case "model/rerouted":
		c.notice("info", fmt.Sprintf("Codex switched from %s to %s (%s)", p.Str("fromModel"), p.Str("toModel"), rerouteReason(p.Str("reason"))))
	case "mcpServer/startupStatus/updated":
		if p.Str("status") == "failed" {
			c.notice("warning", fmt.Sprintf("MCP server %s failed to start: %s", p.Str("name"), orDefault(p.Str("error"), "no reason given")))
		}
	case "hook/completed":
		c.hookDone(p.Obj("run"))
	case "error":
		if !boolField(p, "willRetry") {
			if msg := p.Obj("error").Str("message"); msg != "" {
				c.failure = msg
			}
		}
	case "turn/completed":
		turn := p.Obj("turn")
		if id := turn.Str("id"); id != "" && c.turnID != "" && id != c.turnID {
			return false, nil
		}
		c.status = turn.Str("status")
		if c.status == "failed" {
			if msg := turn.Obj("error").Str("message"); msg != "" {
				c.failure = msg
			}
			if c.failure == "" {
				c.failure = "Codex could not finish the turn"
			}
			return true, errors.New(c.failure)
		}
		return true, nil
	}
	return false, nil
}

// notice shows the user a short message in the turn.
func (c *codexRun) notice(kind, text string) {
	if text = strings.TrimRight(strings.TrimSpace(text), ":"); text != "" {
		c.send(agent.ChatChunk{Notice: &agent.ChatNotice{Kind: kind, Text: text}})
	}
}

// rerouteReason puts Codex's reroute reason in words.
func rerouteReason(reason string) string {
	if reason == "highRiskCyberActivity" {
		return "high-risk cyber activity"
	}
	return orDefault(reason, "no reason given")
}

// hookDone reports a hook that stopped the turn; the others need no word.
func (c *codexRun) hookDone(run codex.Raw) {
	if run.Str("status") != "stopped" {
		return
	}
	var reasons []string
	for _, e := range run.List("entries") {
		if e.Str("kind") == "stop" && strings.TrimSpace(e.Str("text")) != "" {
			reasons = append(reasons, strings.TrimSpace(e.Str("text")))
		}
	}
	text := fmt.Sprintf("A %s hook stopped the turn", run.Str("eventName"))
	if len(reasons) > 0 {
		text += ": " + strings.Join(reasons, "; ")
	}
	c.notice("warning", text)
}

func boolField(r codex.Raw, key string) bool {
	var b bool
	_ = json.Unmarshal(r[key], &b)
	return b
}

func (c *codexRun) agentDelta(itemID, delta string) {
	if delta == "" {
		return
	}
	shown := delta
	if c.streamed[itemID] == 0 {
		shown = c.separator() + delta
	}
	c.streamed[itemID] += len(delta)
	c.text.WriteString(shown)
	c.reply.WriteString(shown)
	c.lastText = true
	c.send(agent.ChatChunk{Delta: shown})
}

// separator keeps two consecutive Codex messages from running together.
func (c *codexRun) separator() string {
	if c.lastText && c.text.Len() > 0 {
		return "\n\n"
	}
	return ""
}

func (c *codexRun) itemStarted(item codex.Raw) {
	switch item.Str("type") {
	case "contextCompaction":
		c.compactionNotice("start")
		return
	case "enteredReviewMode":
		c.notice("info", "Codex started reviewing: "+orDefault(strings.TrimSpace(item.Str("review")), "the changes"))
		return
	case "agentMessage", "plan", "reasoning", "userMessage", "hookPrompt", "sleep", "functionCallOutput":
		return
	}
	t := c.toolFor(item)
	if t == nil {
		return
	}
	c.tools[t.id] = t
	if pid := item.Str("processId"); pid != "" && item.Str("type") == "commandExecution" {
		c.procMu.Lock()
		if c.procs == nil {
			c.procs = map[string]string{}
		}
		c.procs[t.id] = pid
		c.procMu.Unlock()
		// The command may outlive this turn; the thread keeps an eye on it.
		c.thread.watchCommand(t.id, pid)
	}
	c.send(agent.ChatChunk{Tool: &agent.ToolActivity{ID: t.id, Name: t.name, Arguments: t.args, Status: session.ToolRunning}})
}

func (c *codexRun) itemCompleted(item codex.Raw) {
	switch item.Str("type") {
	case "agentMessage":
		// A question asked without stopping the work is a card, not text.
		if card, ok := codexadapter.QuestionCard(item); ok {
			c.questionAsked(card)
			return
		}
		c.messageDone(item)
		return
	case "plan":
		c.messageDone(item)
		return
	case "exitedReviewMode":
		c.notice("info", "Codex finished its review")
		return
	case "contextCompaction":
		c.compactionNotice("done")
		return
	case "userMessage", "hookPrompt", "reasoning", "sleep", "functionCallOutput":
		return
	}
	if item.Str("type") == "fileChange" {
		c.patchDone(item)
		return
	}
	c.procMu.Lock()
	delete(c.procs, item.Str("id"))
	c.procMu.Unlock()
	c.thread.forgetCommand(item.Str("id"))
	t := c.tools[item.Str("id")]
	if t == nil {
		if t = c.toolFor(item); t == nil {
			return
		}
		c.tools[t.id] = t
	}
	result, failed := codexadapter.Outcome(item, t.output.String())
	c.finishTool(t, result, failed)
}

// messageDone makes sure a completed message is fully shown (a message can
// arrive without deltas) and kept.
func (c *codexRun) messageDone(item codex.Raw) {
	id, text := item.Str("id"), item.Str("text")
	if text == "" {
		return
	}
	if already := c.streamed[id]; already < len(text) {
		c.agentDelta(id, text[already:])
	}
}

func (c *codexRun) finishTool(t *codexTool, result string, failed bool) {
	status := session.ToolOK
	if failed {
		status = session.ToolFailed
	}
	c.send(agent.ChatChunk{Tool: &agent.ToolActivity{ID: t.id, Name: t.name, Arguments: t.args, Result: result, Status: status}})
	c.flushText([]transcript.ToolCall{{ID: t.id, Name: t.name, Arguments: t.args}})
	name, id := t.name, t.id
	c.records = append(c.records, transcript.Message{Role: transcript.RoleTool, Content: result,
		CreatedAt: time.Now().UTC(), ToolCallID: &id, Name: &name, IsError: failed})
	delete(c.tools, t.id)
}

// patchDone shows a finished file change the way Paladin shows its own
// edits: one write card per new file and one edit card per changed hunk, so
// the diff renders instead of raw patch text. The first card takes over the
// running card (and the approval card) of the item.
func (c *codexRun) patchDone(item codex.Raw) {
	id := item.Str("id")
	status := item.Str("status")
	failed := status == "failed" || status == "declined"
	var parts []*codexTool
	for _, card := range codexadapter.PatchCards(item) {
		parts = append(parts, &codexTool{name: card.Name, args: card.Args, result: card.Result})
	}
	delete(c.tools, id)
	if len(parts) == 0 {
		parts = []*codexTool{{name: "apply_patch", args: "{}", result: mustJSON(map[string]any{"status": status})}}
	}
	for i, t := range parts {
		t.id = id
		if i > 0 {
			t.id = fmt.Sprintf("%s#%d", id, i+1)
		}
		result := t.result
		if failed {
			msg := "the change failed"
			if status == "declined" {
				msg = "the user declined these changes"
			}
			result = mustJSON(map[string]any{"error": msg})
		}
		c.finishTool(t, result, failed)
	}
}

// closeOpenTools records what the turn ended on. A command Codex still has
// running on a process of its own is background work, not a failure: its card says
// so and the thread settles it when Codex reports how it ended. One that Codex
// stopped without saying how is unknown. Anything else the turn left open was cut off.
func (c *codexRun) closeOpenTools() {
	for _, t := range c.tools {
		if t.name == "shell" && c.status == "completed" {
			c.procMu.Lock()
			_, running := c.procs[t.id]
			c.procMu.Unlock()
			if running {
				t.args = codexadapter.BackgroundArgs(t.args)
				c.finishTool(t, codexadapter.RunningResult(t.output.String()), false)
				continue
			}
			c.finishTool(t, `{"status":"unknown"}`, false)
			continue
		}
		c.thread.forgetCommand(t.id)
		c.finishTool(t, `{"error":"interrupted before it finished"}`, true)
	}
}

// request answers one of Codex's questions to the client: approvals become
// Paladin approval cards, anything else is declined.
func (c *codexRun) request(m codex.Inbound, p codex.Raw) error {
	switch m.Method {
	case "item/commandExecution/requestApproval":
		card := permission.Approval{Tool: "shell", Kind: permission.KindShell, Command: codexadapter.DisplayCommand(p),
			Reasons: reasonList(p.Str("reason"), "Codex wants to run this command"), Choices: codexChoices()}
		if p.Has("networkApprovalContext") {
			card.Reasons = append(card.Reasons, "it needs network access")
		}
		return c.askApproval(m, p, card, func(decision string) any { return map[string]any{"decision": decision} })
	case "item/fileChange/requestApproval":
		card := permission.Approval{Tool: "edit", Kind: permission.KindWrite, Path: p.Str("grantRoot"),
			Reasons: reasonList(p.Str("reason"), "Codex wants to change files"), Choices: codexChoices()}
		return c.askApproval(m, p, card, func(decision string) any { return map[string]any{"decision": decision} })
	case "item/permissions/requestApproval":
		card := permission.Approval{Tool: "shell", Kind: permission.KindPermissions, Path: p.Str("cwd"),
			Reasons: reasonList(p.Str("reason"), "Codex asks for extra permissions"), Choices: []string{permission.ReplyOnce, permission.ReplyDeny}}
		return c.askApproval(m, p, card, func(decision string) any {
			if decision == "accept" {
				return map[string]any{"permissions": json.RawMessage(p.JSON("permissions")), "scope": "turn"}
			}
			return map[string]any{"permissions": map[string]any{}, "scope": "turn"}
		})
	case "item/tool/requestUserInput":
		return c.askQuestions(m, p)
	case "mcpServer/elicitation/request":
		return c.elicitation(m, p)
	}
	return c.client.RespondError(m.RequestID, -32601, "Paladin does not support "+m.Method)
}

// approvalKind groups Codex's approval requests for remembering a "for the
// session" answer. Permission grants are never remembered.
func approvalKind(method string) string {
	switch method {
	case "item/commandExecution/requestApproval":
		return "command"
	case "item/fileChange/requestApproval":
		return "file-change"
	}
	return ""
}

func reasonList(reason, fallback string) []string {
	if strings.TrimSpace(reason) == "" {
		reason = fallback
	}
	if len(reason) > codexApprovalTitleCap {
		reason = reason[:codexApprovalTitleCap] + "…"
	}
	return []string{reason}
}

func codexChoices() []string {
	return []string{permission.ReplyOnce, permission.ReplySession, permission.ReplyDeny}
}

// askApproval shows an approval card and relays the user's answer.
func (c *codexRun) askApproval(m codex.Inbound, p codex.Raw, card permission.Approval, response func(decision string) any) error {
	itemID := p.Str("itemId")
	if itemID == "" {
		itemID = string(m.RequestID)
	}
	// A tool's card sits under its call, so it shares the call's id; a
	// second ask for the same item gets its own.
	card.ID = itemID
	if n := c.approvals[itemID]; n > 0 {
		card.ID = fmt.Sprintf("%s#%d", itemID, n+1)
	}
	c.approvals[itemID]++
	kind := approvalKind(m.Method)
	if c.thread != nil && c.thread.allowance(kind, c.preset) {
		// Already accepted for the session: answer it, and show it as approved.
		card.State, card.Decision = permission.StateApproved, permission.ReplySession
		c.send(agent.ChatChunk{Approval: &card})
		return c.client.Respond(m.RequestID, response("acceptForSession"))
	}
	card.State = permission.StatePending
	ch := c.r.broker.Open(c.sessionID, card.ID)
	c.send(agent.ChatChunk{Approval: &card})
	reply, err := c.r.broker.Wait(c.ctx, c.sessionID, card.ID, ch)
	if err != nil {
		card.State, card.Decision = permission.StateDenied, "interrupted"
		c.send(agent.ChatChunk{Approval: &card})
		_ = c.client.Respond(m.RequestID, response("cancel"))
		// The stop that interrupted the wait is handled by stream.
		return nil //nolint:nilerr
	}
	decision, state := "accept", permission.StateApproved
	switch reply {
	case permission.ReplyDeny:
		decision, state = "decline", permission.StateDenied
	case permission.ReplySession, permission.ReplyFolder, permission.ReplyGlobal:
		decision = "acceptForSession"
	}
	card.State, card.Decision = state, reply
	c.send(agent.ChatChunk{Approval: &card})
	if decision == "acceptForSession" && c.thread != nil {
		c.thread.allow(kind, c.preset)
	}
	if m.Method == "item/permissions/requestApproval" && decision == "acceptForSession" {
		decision = "accept"
	}
	if m.Method == "item/permissions/requestApproval" && decision == "decline" {
		return c.client.Respond(m.RequestID, response("decline"))
	}
	return c.client.Respond(m.RequestID, response(decision))
}
