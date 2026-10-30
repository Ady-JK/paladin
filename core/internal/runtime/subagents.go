// subagents.go runs child agents for a parent session. A child is a
// persistent sibling session (transcript.Meta.Subagent links it back)
// whose turns go through the same hub, permission gate, hooks, sandbox
// and budget as any other turn. The manager tracks which children are
// in flight, delivers each finished child's result to its parent
// exactly once (wait_agent, a mid-turn notice, or an idle wake), and
// lends the parent's hub slot while it waits so a child can run even
// with maxConcurrentTurns = 1.

package runtime

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/Ady-JK/paladin/core/internal/agent"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

// Subagent statuses, persisted in transcript.Subagent.Status.
const (
	AgentQueued      = "queued"
	AgentRunning     = "running"
	AgentCompleted   = "completed"
	AgentFailed      = "failed"
	AgentInterrupted = "interrupted"
)

// Fork modes for spawn_agent.
const (
	ForkNone = "none"
	ForkAll  = "all"
)

const (
	defaultMaxSubagents = 5
	// resultLimit caps a child's result as delivered into the parent's
	// context; the full transcript stays in the child session.
	resultLimit = 6000
)

func finalStatus(s string) bool {
	return s == AgentCompleted || s == AgentFailed || s == AgentInterrupted
}

// AgentInfo is one child as the UI and list_agents see it.
type AgentInfo struct {
	ID              string `json:"agentId"`
	ParentSessionID string `json:"parentSessionId"`
	Name            string `json:"name"`
	Type            string `json:"type"`
	Task            string `json:"task,omitempty"`
	ForkTurns       string `json:"forkTurns,omitempty"`
	Status          string `json:"status"`
	Result          string `json:"result,omitempty"`
	Error           string `json:"error,omitempty"`
	// CompletionID names the result (see transcript.Subagent).
	CompletionID string `json:"completionId,omitempty"`
	UpdatedAt    int64  `json:"updatedAt"`
}

// AgentsEvent is the agents/changed event: a child was spawned or
// changed status.
type AgentsEvent struct {
	Event           string    `json:"event"`
	ParentSessionID string    `json:"parentSessionId"`
	Agent           AgentInfo `json:"agent"`
}

type agentsSink struct{ fn func(AgentsEvent) }

// SetAgentsSink receives every agents/changed event.
func (r *AgentRuntime) SetAgentsSink(fn func(AgentsEvent)) {
	r.subagents.sink.Store(&agentsSink{fn: fn})
}

// turnSettings are the parts of a session's latest turn that its
// children and wake turns inherit.
type turnSettings struct {
	Cwd         string
	Permissions string
	Provider    *string
	Model       *string
}

type subagentManager struct {
	r    *AgentRuntime
	sink atomic.Pointer[agentsSink]

	mu sync.Mutex
	// admit serializes, per parent, the limit and name checks with the
	// child's creation, so parallel spawn_agent calls cannot overshoot
	// either; different parents never wait on each other.
	admit map[string]*sync.Mutex
	// inflight children have a submitted turn whose end is not yet
	// recorded; parentOf maps them (and any child seen) to the parent.
	inflight map[string]bool
	parentOf map[string]string
	// quiet children were stopped by their parent or a parent stop:
	// their end is recorded but not reported back.
	quiet map[string]bool
	// pending is each parent's undelivered child results, in order.
	pending map[string][]string
	// restored parents had their mailbox rebuilt from disk.
	restored map[string]bool
	// changed is closed (and replaced) whenever a parent's child changes.
	changed map[string]chan struct{}
	last    map[string]turnSettings
	// waking parents have a submitted wake turn that has not started.
	waking map[string]bool
	// delivering holds results handed to a parent turn, by turn id,
	// until that turn is saved (then reported) or not (then requeued).
	delivering map[string]delivery
}

// delivery is the child results one parent turn took.
type delivery struct {
	parentID string
	cwd      string
	ids      []string
}

func newSubagentManager(r *AgentRuntime) *subagentManager {
	return &subagentManager{
		r: r, inflight: map[string]bool{}, parentOf: map[string]string{}, quiet: map[string]bool{},
		pending: map[string][]string{}, restored: map[string]bool{}, changed: map[string]chan struct{}{},
		last: map[string]turnSettings{}, waking: map[string]bool{}, delivering: map[string]delivery{},
		admit: map[string]*sync.Mutex{},
	}
}

// admission is the parent's spawn/follow-up gate.
func (m *subagentManager) admission(parentID string) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	gate, ok := m.admit[parentID]
	if !ok {
		gate = &sync.Mutex{}
		m.admit[parentID] = gate
	}
	return gate
}

func (r *AgentRuntime) maxSubagents() int {
	if n := r.loadSettings().MaxSubagents; n > 0 {
		return n
	}
	return defaultMaxSubagents
}

// turnStarted records the settings children inherit and reports
// whether this turn is a wake turn with nothing left to deliver (a
// wait_agent or an earlier turn already took it), which then skips.
func (m *subagentManager) turnStarted(req agent.Request) (skip bool) {
	m.mu.Lock()
	m.last[req.SessionID] = turnSettings{Cwd: req.Cwd, Permissions: req.Permissions, Provider: req.Provider, Model: req.Model}
	wake := m.waking[req.SessionID] && req.Resume
	delete(m.waking, req.SessionID)
	m.mu.Unlock()
	if !wake {
		return false
	}
	return !m.hasPending(req.SessionID, req.Cwd)
}

func (m *subagentManager) settingsFor(sessionID, fallbackCwd string) turnSettings {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.last[sessionID]
	if !ok {
		s.Cwd = fallbackCwd
	}
	return s
}

func (m *subagentManager) emit(parentID string, info AgentInfo) {
	m.mu.Lock()
	if ch, ok := m.changed[parentID]; ok {
		close(ch)
		delete(m.changed, parentID)
	}
	m.mu.Unlock()
	if p := m.sink.Load(); p != nil && p.fn != nil {
		p.fn(AgentsEvent{Event: "agents/changed", ParentSessionID: parentID, Agent: info})
	}
}

// changedChan is closed at the parent's next child change.
func (m *subagentManager) changedChan(parentID string) <-chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch, ok := m.changed[parentID]
	if !ok {
		ch = make(chan struct{})
		m.changed[parentID] = ch
	}
	return ch
}

// status is a child's live status: the hub knows running and queued
// turns; a persisted queued/running status with nothing in flight is
// a turn lost to a restart.
func (m *subagentManager) status(id, persisted string) string {
	switch {
	case m.r.hub.Running(id):
		return AgentRunning
	case m.r.hub.Busy(id):
		return AgentQueued
	}
	m.mu.Lock()
	inflight := m.inflight[id]
	m.mu.Unlock()
	if !inflight && !finalStatus(persisted) {
		return AgentInterrupted
	}
	return persisted
}

func (m *subagentManager) info(child transcript.Session) AgentInfo {
	sa := child.Meta.Subagent
	return AgentInfo{
		ID: child.Meta.SessionID, ParentSessionID: sa.ParentID, Name: sa.Name, Type: sa.Type,
		Task: sa.Task, ForkTurns: sa.ForkTurns, Status: m.status(child.Meta.SessionID, sa.Status),
		Result: sa.Result, Error: sa.Error, CompletionID: sa.CompletionID, UpdatedAt: sa.UpdatedAt.UnixMilli(),
	}
}

func (m *subagentManager) load(cwd, id string) (transcript.Session, error) {
	sess, err := m.r.transcripts.Load(cwd, id)
	if err != nil && cwd != "" {
		sess, err = m.r.transcripts.Load("", id)
	}
	return sess, err
}

// owner loads the calling session, which must be a top-level session:
// children cannot manage agents in this version.
func (m *subagentManager) owner(callerID string) (transcript.Session, error) {
	if strings.TrimSpace(callerID) == "" {
		return transcript.Session{}, errors.New("no calling session")
	}
	settings := m.settingsFor(callerID, "")
	sess, err := m.load(settings.Cwd, callerID)
	if err != nil {
		return transcript.Session{}, err
	}
	if sess.Meta.Subagent != nil {
		return transcript.Session{}, errors.New("subagents cannot spawn or manage other agents")
	}
	return sess, nil
}

// refuseUserTurn rejects a client turn or steer into a subagent
// session: only its parent drives it (via followup_task/send_message).
func (m *subagentManager) refuseUserTurn(sessionID, cwd string) error {
	// A session that does not load yet is new, never a subagent.
	if sess, err := m.load(cwd, sessionID); err == nil && sess.Meta.Subagent != nil {
		return fmt.Errorf("session %s is a subagent of %s: it takes work only from its parent agent", sessionID, sess.Meta.Subagent.ParentID)
	}
	return nil
}

// children loads the parent's live children in spawn order; deleted
// ones are skipped.
func (m *subagentManager) children(parent transcript.Session) []transcript.Session {
	var out []transcript.Session
	for _, id := range parent.Meta.Children {
		child, err := m.load(parent.Meta.Cwd, id)
		if err != nil || child.Meta.Subagent == nil || child.Meta.Subagent.ParentID != parent.Meta.SessionID {
			continue
		}
		out = append(out, child)
	}
	return out
}

// resolve finds one of the parent's children by id or unique name.
func (m *subagentManager) resolve(parent transcript.Session, target string) (transcript.Session, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return transcript.Session{}, errors.New("target is required: an agent_id or name from spawn_agent or list_agents")
	}
	kids := m.children(parent)
	var byName []transcript.Session
	for _, c := range kids {
		if c.Meta.SessionID == target {
			return c, nil
		}
		if c.Meta.Subagent.Name == target {
			byName = append(byName, c)
		}
	}
	switch len(byName) {
	case 1:
		return byName[0], nil
	case 0:
		names := make([]string, 0, len(kids))
		for _, c := range kids {
			names = append(names, c.Meta.Subagent.Name+" ("+c.Meta.SessionID+")")
		}
		known := "none"
		if len(names) > 0 {
			known = strings.Join(names, ", ")
		}
		return transcript.Session{}, fmt.Errorf("unknown agent %q: it is not a subagent of this session (yours: %s)", target, known)
	default:
		return transcript.Session{}, fmt.Errorf("agent name %q is ambiguous; use its agent_id", target)
	}
}

// forget drops a session being deleted from the manager; without its
// last turn settings it can no longer be woken.
func (m *subagentManager) forget(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, mp := range []map[string]bool{m.inflight, m.quiet, m.restored, m.waking} {
		delete(mp, sessionID)
	}
	delete(m.parentOf, sessionID)
	delete(m.pending, sessionID)
	delete(m.last, sessionID)
	delete(m.admit, sessionID)
	if ch, ok := m.changed[sessionID]; ok {
		close(ch)
		delete(m.changed, sessionID)
	}
}

// deleteChildren removes a parent's child sessions with it.
func (m *subagentManager) deleteChildren(parent transcript.Session) error {
	for _, c := range m.children(parent) {
		if _, err := m.r.DeleteSession(c.Meta.SessionID, c.Meta.Cwd); err != nil {
			return err
		}
	}
	return nil
}
