package runtime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/Ady-JK/paladin/core/internal/agent"
	"github.com/Ady-JK/paladin/core/internal/claude"
	"github.com/Ady-JK/paladin/core/internal/codex"
	"github.com/Ady-JK/paladin/core/internal/harness/claudeadapter"
	"github.com/Ady-JK/paladin/core/internal/permission"
	"github.com/Ady-JK/paladin/core/internal/session"
	"github.com/Ady-JK/paladin/core/internal/tools"
	"github.com/Ady-JK/paladin/core/internal/transcript"
	"github.com/google/uuid"
)

const HarnessClaude = "claude"

// claudeCatalog caches what launching the Claude CLI reveals (sign-in, models,
// and per-folder commands). Each launch takes seconds, so a chat switch must
// not pay for it again: see staleCache.
type claudeCatalog struct {
	status   staleCache[claude.Status]
	mu       sync.Mutex
	commands map[string]*staleCache[[]ClaudeCommand]
}

const (
	claudeStatusTTL   = time.Minute
	claudeCommandsTTL = 5 * time.Minute
)

func findHarnessBinary(name string) error {
	var err error
	switch name {
	case HarnessCodex:
		_, err = codex.FindBinary()
	case HarnessClaude:
		_, err = claude.FindBinary()
	default:
		err = fmt.Errorf("unknown harness %q", name)
	}
	return err
}

func (r *AgentRuntime) ClaudeStatus(deep bool) claude.Status {
	st := claude.Status{Models: []claude.Model{}, Permissions: []string{}}
	bin, err := claude.FindBinary()
	if err != nil {
		st.Error = err.Error()
		return st
	}
	st.Installed = true
	if !deep {
		return st
	}
	// A failed load comes back as the status carrying its error and is not kept.
	loaded, _ := r.claudeCatalog.status.get(claudeStatusTTL, func() (claude.Status, error) {
		out := r.loadClaudeStatus(bin)
		if out.Error != "" {
			return out, errors.New(out.Error)
		}
		return out, nil
	})
	return loaded
}

// loadClaudeStatus launches the CLI to read sign-in state and the model list.
func (r *AgentRuntime) loadClaudeStatus(bin string) claude.Status {
	st := claude.Status{Installed: true, Models: []claude.Model{}, Permissions: []string{}}
	ctx, cancel := context.WithTimeout(r.sideCtx, 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "auth", "status")
	data, err := cmd.Output()
	if err != nil {
		st.Error = "Claude Code is not signed in: run `claude auth login` in a terminal"
		return st
	}
	auth := claude.Decode(data)
	st.LoggedIn = auth.Bool("loggedIn")
	st.Account = auth.Str("authMethod")
	if !st.LoggedIn {
		st.Error = "Claude Code is not signed in: run `claude auth login` in a terminal"
		return st
	}
	c, err := claude.Start(ctx, claude.Options{Cwd: os.TempDir(), PermissionMode: "default"}, nil)
	if err != nil {
		st.Error = err.Error()
		return st
	}
	defer c.Close()
	st.Version = c.Info.Str("claude_code_version")
	st.Models = c.Models()
	st.Permissions = []string{"read-only", "full-access"}
	return st
}

// WarmClaude loads the status and the folder's commands in the background, so
// the first switch to a Claude chat finds them already read.
func (r *AgentRuntime) WarmClaude(cwd string) {
	go func() {
		if st := r.ClaudeStatus(true); st.Error != "" {
			return
		}
		_, _ = r.ClaudeCommands(cwd)
	}()
}
func (r *AgentRuntime) claudeThinkingGet(model string) (ThinkingView, error) {
	st := r.ClaudeStatus(true)
	if st.Error != "" {
		return ThinkingView{}, errors.New(st.Error)
	}
	v := ThinkingView{Provider: HarnessClaude, Model: model, Levels: []string{}}
	for _, m := range st.Models {
		if m.ID == model || model == "" && m.IsDefault {
			v.Model = m.ID
			v.Levels = m.Efforts
			break
		}
	}
	v.Level = r.loadSettings().ThinkingLevels[HarnessClaude][v.Model]
	if !containsString(v.Levels, v.Level) {
		v.Level = ""
	}
	return v, nil
}

// claudeRun projects one response and owns its journal; callbacks run on the
// client's reader goroutines, so access to projection is serialized.
type claudeRun struct {
	r               *AgentRuntime
	th              *claudeThread
	sess            transcript.Session
	req             agent.Request
	tx              chan<- agent.ChatChunk
	ctx             context.Context
	mu              sync.Mutex
	p               *claudeadapter.Projection
	preset          permission.Preset
	model           string
	userIDs         map[string]bool
	pendingIDs      map[string]bool
	result          claude.Raw
	callbacks       sync.WaitGroup
	callbackCtx     context.Context
	cancelCallbacks context.CancelFunc
	journal         *transcript.TurnJournal
	userRows        []transcript.Message
	lastCheckpoint  time.Time
	contextTokens   int64
	usageCalls      map[string]claudeCallUsage
	outTokens       int64
}

func (c *claudeRun) send(chunk agent.ChatChunk) {
	chunk.SessionID = c.req.SessionID
	select {
	case c.tx <- chunk:
	case <-c.ctx.Done():
	}
}
func (c *claudeRun) apply(raw claude.Raw) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.noteUsage(raw)
	var todoData string
	if c.th != nil {
		if err := c.th.taskEvent(raw, c); err != nil {
			return err
		}
		var err error
		todoData, err = c.th.todoEvent(raw, c.p, c.sess.Dir)
		if err != nil {
			return err
		}
	}
	for _, chunk := range c.p.Apply(raw) {
		c.send(chunk)
	}
	if todoData != "" {
		c.send(agent.ChatChunk{Tool: &agent.ToolActivity{ID: "claude-plan", Name: "todo", Arguments: todoData, Result: todoData, Status: session.ToolOK}})
	}
	if raw.Str("type") == "system" && raw.Str("subtype") == "init" && c.th != nil {
		if id := raw.Str("session_id"); id != "" && id != c.th.threadID {
			if err := c.r.transcripts.UpdateMeta(c.sess.Meta.Cwd, c.req.SessionID, func(m *transcript.Meta) error {
				m.Harness = &transcript.Harness{Name: HarnessClaude, ThreadID: id}
				return nil
			}); err != nil {
				return err
			}
			c.th.threadID = id
			c.sess.Meta.Harness = &transcript.Harness{Name: HarnessClaude, ThreadID: id}
		}
	}
	if raw.Str("type") == "assistant" && raw.Str("parent_tool_use_id") == "" {
		message := raw.Obj("message")
		if model := message.Str("model"); model != "" {
			c.model = model
		}
		usage := message.Obj("usage")
		if tokens := usage.Int("input_tokens") + usage.Int("cache_read_input_tokens") + usage.Int("cache_creation_input_tokens"); tokens > 0 {
			c.contextTokens = tokens
		}
	}
	if raw.Str("type") == "user" && raw.Str("parent_tool_use_id") == "" {
		c.userIDs[raw.Str("uuid")] = true
	}
	if raw.Str("type") == "user" || time.Since(c.lastCheckpoint) >= time.Second {
		return c.checkpointLocked()
	}
	return nil
}
func (c *claudeRun) control(ctx context.Context, req claude.Raw) (any, error) {
	return c.handleControl(ctx, req)
}

func (c *claudeRun) questions(ctx context.Context, req claude.Raw) (any, error) {
	var asks []tools.AskQuestion
	var original []string
	for _, q := range req.Obj("input").List("questions") {
		var opts []string
		for _, o := range q.List("options") {
			opts = append(opts, o.Str("label"))
		}
		asks = append(asks, tools.AskQuestion{Question: q.Str("question"), Options: opts, Multiple: q.Bool("multiSelect")})
		original = append(original, q.Str("question"))
	}
	args := mustJSON(map[string]any{"questions": asks})
	id := req.Str("tool_use_id")
	result, err := c.r.ask.CallReady(json.RawMessage(args), &tools.ToolCtx{SessionID: c.req.SessionID, CallID: id, Cancel: ctx}, func() {
		c.send(agent.ChatChunk{Tool: &agent.ToolActivity{ID: id, Name: "ask", Arguments: args, Status: session.ToolRunning}})
	})
	if err != nil {
		return map[string]any{"behavior": "deny", "message": "The user dismissed the question"}, nil //nolint:nilerr // Dismissal is a protocol denial, not a transport failure.
	}
	var answered struct {
		Answers []tools.AskAnswer `json:"answers"`
	}
	if err = json.Unmarshal([]byte(result), &answered); err != nil {
		return nil, err
	}
	answers := map[string]any{}
	for i, a := range answered.Answers {
		if i >= len(original) {
			break
		}
		if len(a.Selected) > 1 {
			answers[original[i]] = a.Selected
		} else {
			answers[original[i]] = a.Answer
		}
	}
	c.mu.Lock()
	c.p.Tools[id] = claudeadapter.Tool(id, "AskUserQuestion", claude.Decode([]byte(args)))
	t := c.p.CompleteTool(id, result, false)
	c.mu.Unlock()
	c.send(agent.ChatChunk{Tool: t})
	return map[string]any{"behavior": "allow", "updatedInput": map[string]any{"questions": req.Obj("input")["questions"], "answers": answers}}, nil
}

func (r *AgentRuntime) runClaudeTurn(ctx context.Context, sess transcript.Session, req agent.Request, workspace string, gate func(func() error), tx chan<- agent.ChatChunk) (agent.ChatOutcome, error) {
	preset, err := permission.ParsePreset(req.Permissions)
	if err != nil {
		return agent.ChatOutcome{}, err
	}
	history, err := sess.History()
	if err != nil {
		return agent.ChatOutcome{}, err
	}
	index := len(history)
	started := time.Now()
	message := req.Message
	if req.Compact {
		message = "/compact " + message
	}
	if req.Command != nil {
		message = "/" + *req.Command + " " + message
	}
	if req.Skill != nil {
		message = "/" + *req.Skill + " " + message
	}
	userID := uuid.NewString()
	var userRows []transcript.Message
	if !req.Resume {
		user := transcript.UserMessage(message)
		user.Display = req.Display
		user.Attachments = req.Attachments
		user.HarnessTurn = userID
		userRows = []transcript.Message{user}
	}
	journal, err := sess.BeginJournal(userRows)
	if err != nil {
		return agent.ChatOutcome{}, err
	}
	defer journal.Release()
	model := derefString(req.Model)
	if model == "" {
		model = derefString(sess.Meta.Model)
	}
	callbackCtx, cancelCallbacks := context.WithCancel(ctx)
	defer cancelCallbacks()
	run := &claudeRun{r: r, sess: sess, req: req, tx: tx, ctx: ctx, p: claudeadapter.NewProjection(), preset: preset, model: model, userIDs: map[string]bool{userID: true}, pendingIDs: map[string]bool{}, callbackCtx: callbackCtx, cancelCallbacks: cancelCallbacks, journal: journal, userRows: userRows}
	th, fresh, err := r.claudeThreadFor(ctx, &sess, run, workspace, model)
	if err == nil {
		run.th = th
		if !req.Resume {
			if fresh && len(history) > 0 {
				message = "Continue this earlier conversation:\n" + priorContext(history) + "\n" + message
			}
			var content any = message
			if len(req.Attachments) > 0 {
				blocks := []map[string]any{{"type": "text", "text": message}}
				for _, a := range req.Attachments {
					if a.Image != nil {
						b, e := os.ReadFile(a.Path)
						if e != nil {
							err = e
							break
						}
						blocks = append(blocks, map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": a.Image.MIMEType, "data": base64.StdEncoding.EncodeToString(b)}})
					}
				}
				content = blocks
			}
			if err == nil {
				run.pendingIDs[userID] = true
				err = th.client.Prompt(content, userID)
			}
		}
		if err == nil {
			err = run.stream()
		}
		if ctx.Err() != nil {
			th.client.Close()
		}
		th.release()
	}
	cancelCallbacks()
	run.callbacks.Wait()
	run.mu.Lock()
	for id := range run.p.Tools {
		if t := run.p.CompleteTool(id, `{"error":"interrupted before completion"}`, true); t != nil {
			run.send(agent.ChatChunk{Tool: t})
		}
	}
	run.p.Flush(nil)
	rows := append([]transcript.Message{}, userRows...)
	rows = append(rows, run.p.Records...)
	reply := run.p.Reply.String()
	run.mu.Unlock()
	stats := &transcript.TurnStats{DurationMs: time.Since(started).Milliseconds(), Tokens: run.outTokens, Incomplete: err != nil || ctx.Err() != nil}
	var persistErr error
	gate(func() error {
		persistErr = sess.AppendJournalTurn(rows, stats, journal.ID)
		if persistErr == nil {
			persistErr = journal.Finish()
		}
		return persistErr
	})
	if persistErr != nil {
		return agent.ChatOutcome{}, persistErr
	}
	if th != nil {
		if err := th.flushTasks(); err != nil {
			return agent.ChatOutcome{}, err
		}
	}
	out := agent.ChatOutcome{SessionID: req.SessionID, Reply: reply, Stats: stats}
	if !req.Resume {
		out.Index = &index
	}
	if ctx.Err() != nil {
		out.Stopped = true
		r.emitDone(tx, req.SessionID, true, nil)
		return out, nil
	}
	if err != nil {
		r.emitDone(tx, req.SessionID, false, agent.NewApiError("internal", err.Error()))
		return out, err
	}
	r.emitDone(tx, req.SessionID, false, nil)
	return out, nil
}
func (c *claudeRun) stream() error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	interrupted := false
	var deadline <-chan time.Time
	done := c.ctx.Done()
	for {
		for {
			raw, ok := c.th.sub.Next()
			if !ok {
				break
			}
			if err := c.apply(raw); err != nil {
				return err
			}
			if raw.Str("type") == "result" {
				c.result = raw
				if err := c.recordUsage(raw); err != nil {
					return err
				}
				if raw.Bool("is_error") || raw.Str("subtype") != "success" {
					text := strings.Join(raw.Strings("errors"), "; ")
					if text == "" {
						text = raw.Str("result")
					}
					if text == "" {
						text = raw.Str("subtype")
					}
					return errors.New(text)
				}
				answered := raw.Strings("user_message_uuids")
				if len(answered) > 0 {
					for _, id := range answered {
						delete(c.pendingIDs, id)
					}
				} else {
					for id := range c.userIDs {
						delete(c.pendingIDs, id)
					}
				}
				if len(c.pendingIDs) == 0 {
					return nil
				}
			}
		}
		select {
		case <-c.th.sub.Wake():
		case <-c.th.client.Done():
			if e := c.th.client.Err(); e != nil {
				return e
			}
			return claude.ErrClosed
		case <-done:
			done = nil
			interrupted = true
			deadline = time.After(8 * time.Second)
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_, _ = c.th.client.Call(ctx, map[string]any{"subtype": "interrupt"})
			}()
		case <-deadline:
			c.th.client.Close()
			return context.Canceled
		case <-ticker.C:
			if !interrupted && c.req.Steer != nil {
				for _, m := range c.req.Steer() {
					if strings.TrimSpace(m.Message) == "" {
						continue
					}
					id := uuid.NewString()
					if err := c.th.client.Prompt(m.Message, id); err != nil {
						return err
					}
					c.pendingIDs[id] = true
					c.mu.Lock()
					c.p.Flush(nil)
					rec := transcript.UserMessage(m.Message)
					rec.Display = m.Display
					rec.HarnessTurn = id
					c.p.Records = append(c.p.Records, rec)
					c.mu.Unlock()
					c.send(agent.ChatChunk{Steer: &m})
				}
			}
		}
	}
}
func (c *claudeRun) recordUsage(result claude.Raw) error {
	return c.persistClaudeUsage(result)
}
