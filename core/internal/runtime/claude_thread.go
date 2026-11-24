package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Ady-JK/paladin/core/internal/agent"
	"github.com/Ady-JK/paladin/core/internal/claude"
	"github.com/Ady-JK/paladin/core/internal/transcript"
	"github.com/google/uuid"
)

type claudeThread struct {
	r                                             *AgentRuntime
	sessionID, cwd, threadID, model, effort, mode string
	actMode                                       string // the mode Claude works in outside plan mode
	client                                        *claude.Client
	sub                                           *claude.Subscription
	mu                                            sync.Mutex
	run                                           *claudeRun
	adopting                                      bool
	cost                                          float64
	costModels                                    map[string]float64
	kick                                          chan struct{}
	ready                                         chan struct{}
	tasks                                         map[string]string
	taskResults                                   map[string]claude.Raw
	todos                                         map[string]claudeTodoItem
}

func (r *AgentRuntime) claudeThread(id string) *claudeThread {
	r.claudeMu.Lock()
	defer r.claudeMu.Unlock()
	return r.claudeThreads[id]
}

func (r *AgentRuntime) claudeDrop(id string) {
	r.claudeMu.Lock()
	delete(r.claudeThreads, id)
	r.claudeMu.Unlock()
}

func (r *AgentRuntime) claudePut(id string, th *claudeThread) {
	r.claudeMu.Lock()
	if r.claudeThreads == nil {
		r.claudeThreads = map[string]*claudeThread{}
	}
	r.claudeThreads[id] = th
	r.claudeMu.Unlock()
}

func (r *AgentRuntime) claudeThreadFor(ctx context.Context, sess *transcript.Session, run *claudeRun, workspace, model string) (*claudeThread, bool, error) {
	id := sess.Meta.SessionID
	// Starting a CLI and its first calls take seconds: only this session waits,
	// and the map itself is locked just for each lookup.
	defer r.claudeStart.lock(id)()
	remaining, err := r.claudeBudget(*sess)
	if err != nil {
		return nil, false, err
	}
	if remaining > 0 && !run.req.Resume {
		if old := r.claudeThread(id); old != nil {
			old.client.Close()
			r.claudeDrop(id)
		}
	}
	effort := r.loadSettings().ThinkingLevels[HarnessClaude][model]
	plan := run.req.Mode == "plan"
	if th := r.claudeThread(id); th != nil {
		select {
		case <-th.client.Done():
			r.claudeDrop(id)
		default:
			th.mu.Lock()
			if th.run != nil {
				th.mu.Unlock()
				return nil, false, errors.New("claude session already has an active turn")
			}
			th.run = run
			th.adopting = false
			if th.ready != nil {
				close(th.ready)
				th.ready = nil
			}
			th.mu.Unlock()
			mode := claude.ModePlan
			if !plan {
				mode = th.actMode
			}
			// Effort is startup-only; a change restarts the CLI and resumes its saved
			// conversation. Never restart an autonomous continuation.
			if effort != th.effort && !run.req.Resume {
				th.client.Close()
				r.claudeDrop(id)
			} else {
				cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
				defer cancel()
				if model != th.model {
					if _, err := th.client.Call(cctx, map[string]any{"subtype": "set_model", "model": model}); err != nil {
						th.release()
						return nil, false, err
					}
					th.model = model
				}
				if run.req.Resume {
					mode = th.mode
				}
				if mode != th.mode {
					if _, err := th.client.Call(cctx, map[string]any{"subtype": "set_permission_mode", "mode": mode}); err != nil {
						th.release()
						return nil, false, err
					}
					th.mode = mode
				}
				return th, false, nil
			}
		}
	}
	threadID := sess.Meta.Harness.ThreadID
	fresh := threadID == ""
	// Start in bypass: the CLI reports what it actually granted, and ActingMode
	// falls back to auto when the organisation disables bypass.
	opts := claude.Options{Cwd: workspace, Model: model, Effort: effort, PermissionMode: claude.ModeBypass, SystemPrompt: derefString(run.req.SystemPrompt), Hooks: map[string]any{"PreToolUse": []map[string]any{{"matcher": "", "hookCallbackIds": []string{"paladin-permission"}, "onFailure": "block"}}}}
	opts.MaxBudget = remaining
	if fresh {
		threadID = uuid.NewString()
		if fork := sess.Meta.Harness.ForkOf; fork != nil {
			boundary, err := claude.ForkBoundary(fork.ThreadID, fork.LastTurn)
			if err != nil {
				return nil, false, err
			}
			opts.Resume, opts.ResumeAt, opts.ForkSession = fork.ThreadID, boundary, true
		} else {
			opts.SessionID = threadID
		}
	} else {
		opts.Resume = threadID
	}
	th := &claudeThread{r: r, sessionID: id, cwd: sess.Meta.Cwd, threadID: threadID, model: model, effort: effort, run: run, kick: make(chan struct{}, 1)}

	cctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	client, err := claude.Start(cctx, opts, th.control)
	if err != nil {
		return nil, false, err
	}
	th.client, th.sub = client, client.Subscription()
	th.actMode = client.ActingMode(cctx)
	th.mode = th.actMode
	if plan {
		if _, err := client.Call(cctx, map[string]any{"subtype": "set_permission_mode", "mode": claude.ModePlan}); err != nil {
			client.Close()
			return nil, false, err
		}
		th.mode = claude.ModePlan
	}
	if fresh {
		if err = r.transcripts.UpdateMeta(sess.Meta.Cwd, id, func(m *transcript.Meta) error {
			m.Harness = &transcript.Harness{Name: HarnessClaude, ThreadID: threadID}
			return nil
		}); err != nil {
			client.Close()
			return nil, false, err
		}
		sess.Meta.Harness = &transcript.Harness{Name: HarnessClaude, ThreadID: threadID}
	}
	r.claudePut(id, th)
	go th.watch()
	return th, fresh && !opts.ForkSession, nil
}
func (th *claudeThread) control(ctx context.Context, request claude.Raw) (any, error) {
	for {
		th.mu.Lock()
		run := th.run
		if run != nil {
			run.callbacks.Add(1)
			th.mu.Unlock()
			defer run.callbacks.Done()
			cctx, cancel := context.WithCancel(ctx)
			defer cancel()
			stop := context.AfterFunc(run.callbackCtx, cancel)
			defer stop()
			return run.control(cctx, request)
		}
		if th.ready == nil {
			th.ready = make(chan struct{})
		}
		ready := th.ready
		start := !th.adopting
		th.adopting = true
		th.mu.Unlock()
		if start {
			if err := th.adopt(); err != nil {
				th.mu.Lock()
				th.adopting = false
				th.mu.Unlock()
				return nil, err
			}
		}
		select {
		case <-ready:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}
func (th *claudeThread) release() {
	th.mu.Lock()
	run := th.run
	th.run = nil
	if th.ready == nil {
		th.ready = make(chan struct{})
	}
	th.mu.Unlock()
	if run != nil {
		run.cancelCallbacks()
		run.callbacks.Wait()
	}
	select {
	case th.kick <- struct{}{}:
	default:
	}
}
func (th *claudeThread) adopt() error {
	settings := th.r.subagents.settingsFor(th.sessionID, th.cwd)
	provider := HarnessClaude
	_, done, err := th.r.hub.Submit(agent.Request{SessionID: th.sessionID, Cwd: th.cwd, Provider: &provider, Model: settings.Model, Permissions: settings.Permissions, Mode: settings.Mode, Resume: true}, SubmitOpts{})
	if err != nil {
		return err
	}
	go func() {
		<-done
		th.mu.Lock()
		failed := th.adopting && th.run == nil
		th.mu.Unlock()
		if failed {
			// A cancelled queued adoption or startup failure never acquired the
			// stream. Retire it so callbacks cannot wait on readiness forever.
			th.client.Close()
			th.mu.Lock()
			th.adopting = false
			if th.ready != nil {
				close(th.ready)
				th.ready = nil
			}
			th.mu.Unlock()
		}
	}()
	return nil
}

func (th *claudeThread) watch() {
	for {
		select {
		case <-th.sub.Wake():
		case <-th.kick:
		case <-th.client.Done():
			return
		}
		for {
			th.mu.Lock()
			if th.run != nil || th.adopting {
				th.mu.Unlock()
				break
			}
			raw, ok := th.sub.Peek()
			if !ok {
				th.mu.Unlock()
				break
			}
			if raw.Str("type") == "assistant" || raw.Str("type") == "stream_event" {
				th.adopting = true
				th.mu.Unlock()
				if err := th.adopt(); err != nil {
					th.mu.Lock()
					th.adopting = false
					th.mu.Unlock()
					_, _ = th.sub.Next()
				}
				break
			}
			_, _ = th.sub.Next()
			th.mu.Unlock()
			if err := th.taskEvent(raw, nil); err != nil {
				th.r.hub.PublishSession(th.sessionID, "session/refresh")
			}
		}
	}
}
func (r *AgentRuntime) forgetClaudeThread(id string) {
	r.claudeMu.Lock()
	th := r.claudeThreads[id]
	delete(r.claudeThreads, id)
	r.claudeMu.Unlock()
	if th != nil {
		th.client.Close()
	}
}
func (r *AgentRuntime) closeClaudeThreads() {
	r.claudeMu.Lock()
	threads := r.claudeThreads
	r.claudeThreads = map[string]*claudeThread{}
	r.claudeMu.Unlock()
	var wg sync.WaitGroup
	for _, th := range threads {
		wg.Add(1)
		go func() { defer wg.Done(); th.client.Close() }()
	}
	wg.Wait()
}

// A new conversation after a rewind must never resume the abandoned tail.
// The next turn seeds a fresh CLI conversation from Paladin's retained history.
func (r *AgentRuntime) claudeRewind(sess *transcript.Session, index int) error {
	if sess.Meta.Harness == nil || sess.Meta.Harness.Name != HarnessClaude {
		return nil
	}
	history, err := sess.History()
	if err != nil {
		return err
	}
	var last string
	for _, message := range history[:min(index, len(history))] {
		if message.HarnessTurn != "" {
			last = message.HarnessTurn
		}
	}
	var fork *transcript.HarnessFork
	if last != "" && sess.Meta.Harness.ThreadID != "" {
		fork = &transcript.HarnessFork{ThreadID: sess.Meta.Harness.ThreadID, LastTurn: last}
	}
	r.forgetClaudeThread(sess.Meta.SessionID)
	sess.Meta.Harness.ForkOf = fork
	if err := r.transcripts.UpdateMeta(sess.Meta.Cwd, sess.Meta.SessionID, func(m *transcript.Meta) error { m.Harness.ThreadID = ""; m.Harness.ForkOf = fork; return nil }); err != nil {
		return fmt.Errorf("reset Claude conversation for rewind at %d: %w", index, err)
	}
	sess.Meta.Harness.ThreadID = ""
	sess.Meta.Harness.ForkOf = fork
	return nil
}
