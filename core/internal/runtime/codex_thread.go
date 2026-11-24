package runtime

import (
	"context"
	"fmt"
	"log"
	"sync"

	"github.com/Ady-JK/paladin/core/internal/agent"
	"github.com/Ady-JK/paladin/core/internal/codex"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

// codexThread is one Codex session's live connection: its thread on the shared
// app-server and the subscription that carries everything Codex says about it.
// The subscription outlives any one turn, because Codex starts turns on its
// own (a goal continues, a background task finishes): while no turn of ours is
// reading it, the watcher below adopts such a turn as a Paladin turn.
type codexThread struct {
	r         *AgentRuntime
	sessionID string
	cwd       string
	threadID  string
	client    *codex.Client
	sub       *codex.Subscription
	model     string // what Codex resolved for the thread (its default when none was chosen)
	// usageBase is the thread's cumulative token total at its last usage
	// notice; the next notice bills the difference.
	usageBase *codexTokens
	// allowed remembers, per kind of request (a command, a file change), the
	// permission preset in force when the user accepted it for the session:
	// Codex's own "for the session" covers only the exact command, so Paladin
	// answers the later ones itself until the preset changes.
	allowed map[string]string
	// storeCwd is the folder the session is stored under, and bg the commands
	// Codex runs on processes of their own, which can outlive a turn.
	storeCwd string
	bg       bgState

	mu       sync.Mutex
	owned    bool // a turn is reading sub
	adopting bool // an adopting turn is queued and has not started reading
	adoptGen int  // counts adoptions, so a late finish cannot clear a newer one
	kick     chan struct{}
}

// codexThreadFor returns the session's live thread, starting or resuming it on
// the shared app-server when this process has none yet. fresh reports a thread
// that was just started for a session that may already have history (a fork).
func (r *AgentRuntime) codexThreadFor(ctx context.Context, sess *transcript.Session, workspace string, params codex.ThreadParams) (th *codexThread, fresh bool, err error) {
	client, err := r.codex.Client(ctx)
	if err != nil {
		return nil, false, err
	}
	id := sess.Meta.SessionID
	threadID := ""
	if sess.Meta.Harness != nil {
		threadID = sess.Meta.Harness.ThreadID
	}
	// Forking, starting and resuming a thread are app-server round trips of up
	// to seconds: only this session waits, and the map is locked per lookup.
	defer r.codexStart.lock(id)()
	if existing := r.codexThread(id); existing != nil && existing.client == client && existing.threadID == threadID && threadID != "" {
		return existing, false, nil
	}
	if old := r.codexThread(id); old != nil {
		old.sub.Close()
		r.forgetCodexThreadEntry(id, old)
	}
	if threadID == "" {
		// A fork continues from its source's thread when Codex can fork it;
		// otherwise it starts a thread that is told the conversation.
		if fork := forkSource(sess); fork != nil {
			if threadID, err = client.ForkThread(ctx, fork.ThreadID, fork.LastTurn, params); err != nil {
				log.Printf("codex: forking %s for %s: %v; starting a new thread instead", fork.ThreadID, id, err)
				threadID = ""
			}
		}
		if threadID == "" {
			if threadID, err = client.StartThread(ctx, params); err != nil {
				return nil, false, err
			}
			fresh = true
		}
		if err := r.transcripts.UpdateMeta(sess.Meta.Cwd, id, func(m *transcript.Meta) error {
			m.Harness = &transcript.Harness{Name: HarnessCodex, ThreadID: threadID}
			return nil
		}); err != nil {
			return nil, false, fmt.Errorf("save codex thread: %w", err)
		}
		sess.Meta.Harness = &transcript.Harness{Name: HarnessCodex, ThreadID: threadID}
	} else if err := client.ResumeThread(ctx, threadID, params); err != nil {
		return nil, false, err
	}
	sub, err := client.Subscribe(threadID)
	if err != nil {
		return nil, false, err
	}
	th = &codexThread{r: r, sessionID: id, storeCwd: sess.Meta.Cwd, cwd: workspace, threadID: threadID, client: client, sub: sub, model: client.ThreadModel(threadID), kick: make(chan struct{}, 1)}
	r.codexMu.Lock()
	r.codexThreads[id] = th
	r.codexMu.Unlock()
	go th.watch()
	return th, fresh, nil
}

// forkSource is the thread a not-yet-started forked session continues from.
func forkSource(sess *transcript.Session) *transcript.HarnessFork {
	if sess.Meta.Harness == nil {
		return nil
	}
	return sess.Meta.Harness.ForkOf
}

func (r *AgentRuntime) codexThread(id string) *codexThread {
	r.codexMu.Lock()
	defer r.codexMu.Unlock()
	return r.codexThreads[id]
}

// forgetCodexThreadEntry removes id's entry if it is still th.
func (r *AgentRuntime) forgetCodexThreadEntry(id string, th *codexThread) {
	r.codexMu.Lock()
	if r.codexThreads[id] == th {
		delete(r.codexThreads, id)
	}
	r.codexMu.Unlock()
}

// forgetCodexThread drops a session's thread (its session was deleted, or the
// thread was replaced).
func (r *AgentRuntime) forgetCodexThread(sessionID string) {
	r.codexMu.Lock()
	th := r.codexThreads[sessionID]
	delete(r.codexThreads, sessionID)
	r.codexMu.Unlock()
	if th != nil {
		th.sub.Close()
	}
}

// acquire makes the caller the subscription's reader.
func (th *codexThread) acquire() error {
	th.mu.Lock()
	defer th.mu.Unlock()
	if th.owned {
		return fmt.Errorf("codex thread %s already has a turn running", th.threadID)
	}
	th.owned, th.adopting = true, false
	return nil
}

func (th *codexThread) release() {
	th.mu.Lock()
	th.owned = false
	th.mu.Unlock()
	select {
	case th.kick <- struct{}{}:
	default:
	}
}

// watch looks after the subscription while no turn reads it.
func (th *codexThread) watch() {
	for {
		select {
		case <-th.sub.Wake():
		case <-th.kick:
		case <-th.client.Done():
			th.r.codexMu.Lock()
			if th.r.codexThreads[th.sessionID] == th {
				delete(th.r.codexThreads, th.sessionID)
			}
			th.r.codexMu.Unlock()
			th.sub.Close()
			return
		}
		if th.sub.Done() {
			return
		}
		th.drainIdle()
	}
}

// drainIdle consumes what Codex says between our turns. A turn Codex started
// by itself becomes a Paladin turn (left in the queue for it to read);
// everything else is dropped, and a stray request is refused so Codex does
// not wait for it.
func (th *codexThread) drainIdle() {
	th.mu.Lock()
	defer th.mu.Unlock()
	for !th.owned && !th.adopting {
		m, ok, _ := th.sub.Peek()
		if !ok {
			return
		}
		if m.Method == "turn/started" {
			th.adopting = true
			if err := th.adoptTurn(); err != nil {
				log.Printf("codex: adopting a turn of %s: %v", th.sessionID, err)
				th.adopting = false
				th.sub.Pop()
			}
			return
		}
		th.sub.Pop()
		if m.IsRequest() {
			_ = th.client.RespondError(m.RequestID, -32601, "paladin has no turn waiting for this request")
			continue
		}
		// Between turns, the one thing worth keeping is a command's ending.
		th.noteLate(m.Method, codex.Decode(m.Params))
	}
}

// adoptTurn submits a resume turn (no user message) for a turn Codex started.
func (th *codexThread) adoptTurn() error {
	settings := th.r.subagents.settingsFor(th.sessionID, th.cwd)
	provider := HarnessCodex
	req := agent.Request{SessionID: th.sessionID, Cwd: settings.Cwd, Resume: true, Permissions: settings.Permissions,
		Provider: &provider, Model: settings.Model}
	_, done, err := th.r.hub.Submit(req, SubmitOpts{})
	if err != nil {
		return err
	}
	th.adoptGen++ // the caller holds th.mu
	gen := th.adoptGen
	go func() {
		<-done
		th.mu.Lock()
		failed := th.adopting && !th.owned && th.adoptGen == gen
		if failed {
			// The turn was cancelled in the queue or failed before it took the
			// thread. Drop the start it was meant to read and go back to
			// draining, or Codex's messages would pile up unanswered.
			th.adopting = false
			if m, ok, _ := th.sub.Peek(); ok && m.Method == "turn/started" {
				th.sub.Pop()
			}
		}
		th.mu.Unlock()
		if failed {
			select {
			case th.kick <- struct{}{}:
			default:
			}
		}
	}()
	return nil
}

// codexThreadsClose ends every thread's subscription (shutdown).
func (r *AgentRuntime) codexThreadsClose() {
	r.codexMu.Lock()
	threads := r.codexThreads
	r.codexThreads = map[string]*codexThread{}
	r.codexMu.Unlock()
	for _, th := range threads {
		th.sub.Close()
	}
}

// discardIdle drops what is queued from before the turn about to start.
func (th *codexThread) discardIdle() {
	for {
		m, ok, _ := th.sub.Peek()
		if !ok || m.Method == "turn/started" {
			return
		}
		th.sub.Pop()
		if m.IsRequest() {
			_ = th.client.RespondError(m.RequestID, -32601, "paladin has no turn waiting for this request")
		}
	}
}

// allowance reports whether requests of kind are already accepted for the
// session under preset.
func (th *codexThread) allowance(kind, preset string) bool {
	th.mu.Lock()
	defer th.mu.Unlock()
	return kind != "" && th.allowed[kind] == preset
}

func (th *codexThread) allow(kind, preset string) {
	if kind == "" {
		return
	}
	th.mu.Lock()
	defer th.mu.Unlock()
	if th.allowed == nil {
		th.allowed = map[string]string{}
	}
	th.allowed[kind] = preset
}
