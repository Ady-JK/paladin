// subagent_lifecycle.go starts, steers, stops and records child agents:
// spawn and follow-up (admitted per parent under the child limit), the
// child's queued turn, and the bookkeeping when that turn ends.

package runtime

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/Ady-JK/paladin/core/internal/agent"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

// spawnArgs are a validated spawn_agent call.
type spawnArgs struct {
	Kind      subagentKind
	Task      string
	Name      string
	ForkTurns string
	Model     string
	CallID    string
}

// spawn creates the child session and queues its first turn.
func (m *subagentManager) spawn(parentID string, a spawnArgs) (AgentInfo, error) {
	gate := m.admission(parentID)
	gate.Lock()
	defer gate.Unlock()
	parent, err := m.owner(parentID)
	if err != nil {
		return AgentInfo{}, err
	}
	active, names := m.activeChildren(parent, "")
	if err := m.checkLimit(active); err != nil {
		return AgentInfo{}, err
	}
	name := uniqueName(a.Name, a.Kind.Name, names)
	var snapshot []transcript.Message
	if a.ForkTurns == ForkAll {
		if snapshot, err = finishedHistory(parent); err != nil {
			return AgentInfo{}, err
		}
	}
	settings := m.settingsFor(parentID, parent.Meta.Cwd)
	link := transcript.Subagent{
		ParentTurnID: m.r.hub.RunningTurnID(parentID), SpawnCallID: a.CallID,
		Type: a.Kind.Name, Name: name, Task: a.Task, ForkTurns: a.ForkTurns, Status: AgentQueued,
	}
	// The child runs where the parent's current turn runs: the turn's
	// provider and model are per request, not necessarily in its meta.
	model := a.Model
	if model == "" {
		model = derefString(settings.Model)
	}
	child, err := m.r.transcripts.CreateChild(parentID, settings.Cwd, link, snapshot, derefString(settings.Provider), model)
	if err != nil {
		return AgentInfo{}, err
	}
	m.mu.Lock()
	m.parentOf[child.Meta.SessionID] = parentID
	m.mu.Unlock()
	if err := m.submit(child, settings, a.Task); err != nil {
		return AgentInfo{}, err
	}
	info := m.info(child)
	m.emit(parentID, info)
	return info, nil
}

// activeChildren counts the parent's queued or running children other
// than except, and collects every child's name.
func (m *subagentManager) activeChildren(parent transcript.Session, except string) (int, map[string]bool) {
	active := 0
	names := map[string]bool{}
	for _, c := range m.children(parent) {
		names[c.Meta.Subagent.Name] = true
		if c.Meta.SessionID != except && !finalStatus(m.status(c.Meta.SessionID, c.Meta.Subagent.Status)) {
			active++
		}
	}
	return active, names
}

func (m *subagentManager) checkLimit(active int) error {
	if limit := m.r.maxSubagents(); active >= limit {
		return fmt.Errorf("subagent limit reached: %d already queued or running (max %d); wait_agent for one to finish or interrupt_agent one", active, limit)
	}
	return nil
}

// finishedHistory is the parent's persisted conversation: only turns
// that finished (the running turn persists at its end), cut at a
// pending undo like a fork.
func finishedHistory(parent transcript.Session) ([]transcript.Message, error) {
	msgs, err := parent.History()
	if err != nil {
		return nil, err
	}
	if rv := parent.Meta.Revert; rv != nil && rv.Index >= 0 && rv.Index < len(msgs) {
		msgs = msgs[:rv.Index]
	}
	return msgs, nil
}

func uniqueName(want, kind string, taken map[string]bool) string {
	base := strings.Join(strings.Fields(want), " ")
	if base == "" {
		base = kind
	}
	if !taken[base] && want != "" {
		return base
	}
	for i := 1; ; i++ {
		name := fmt.Sprintf("%s-%d", base, i)
		if !taken[name] {
			return name
		}
	}
}

// submit queues a child turn. The persisted status is set before the
// turn exists, so the turn's own end can never be overwritten by it.
// The child runs on the provider and model stored in its own meta.
func (m *subagentManager) submit(child transcript.Session, settings turnSettings, task string) error {
	childID := child.Meta.SessionID
	m.mu.Lock()
	m.inflight[childID] = true
	m.mu.Unlock()
	_, _, err := m.r.hub.Submit(agent.Request{
		SessionID: childID, Cwd: child.Meta.Cwd, Message: task,
		Provider: child.Meta.Provider, Model: child.Meta.Model,
		Permissions: settings.Permissions, Mode: agent.ModeBuild,
	}, SubmitOpts{Display: task, ParentSessionID: child.Meta.Subagent.ParentID})
	if err != nil {
		m.mu.Lock()
		delete(m.inflight, childID)
		m.mu.Unlock()
		m.logged("record failed submit", m.record(childID, settings.Cwd, outcome{Status: AgentFailed, Error: err.Error(), Reported: true}))
		return err
	}
	return nil
}

// outcome is a child's recorded state.
type outcome struct {
	Status       string
	Result       string
	Error        string
	CompletionID string
	Reported     bool
}

func (m *subagentManager) record(childID, cwd string, o outcome) error {
	return m.r.transcripts.UpdateMeta(cwd, childID, func(meta *transcript.Meta) error {
		sa := meta.Subagent
		if sa == nil {
			return errors.New("not a subagent session")
		}
		sa.Status, sa.Result, sa.Error = o.Status, o.Result, o.Error
		sa.CompletionID, sa.Reported = o.CompletionID, o.Reported
		sa.UpdatedAt = time.Now().UTC()
		return nil
	})
}

// logged reports a failed bookkeeping write. The caller carries on: the
// child's transcript is intact, only its status or delivery mark is
// stale, and a restart reconciles delivery from the parent transcript.
func (m *subagentManager) logged(what string, err error) {
	if err != nil {
		log.Printf("subagents: %s: %v", what, err)
	}
}

// followup gives an existing child another task.
func (m *subagentManager) followup(parentID, target, message string) (AgentInfo, error) {
	gate := m.admission(parentID)
	gate.Lock()
	defer gate.Unlock()
	parent, err := m.owner(parentID)
	if err != nil {
		return AgentInfo{}, err
	}
	child, err := m.resolve(parent, target)
	if err != nil {
		return AgentInfo{}, err
	}
	id := child.Meta.SessionID
	// Reactivating a finished child takes a slot under the limit.
	if finalStatus(m.status(id, child.Meta.Subagent.Status)) {
		active, _ := m.activeChildren(parent, id)
		if err := m.checkLimit(active); err != nil {
			return AgentInfo{}, err
		}
	}
	settings := m.settingsFor(parentID, parent.Meta.Cwd)
	if err := m.record(id, child.Meta.Cwd, outcome{Status: AgentQueued}); err != nil {
		return AgentInfo{}, err
	}
	m.mu.Lock()
	m.parentOf[id] = parentID
	delete(m.quiet, id)
	m.dropPendingLocked(parentID, id)
	m.mu.Unlock()
	if err := m.submit(child, settings, message); err != nil {
		return AgentInfo{}, err
	}
	child, err = m.load(child.Meta.Cwd, id)
	if err != nil {
		return AgentInfo{}, err
	}
	info := m.info(child)
	m.emit(parentID, info)
	return info, nil
}

// message steers a running child.
func (m *subagentManager) message(parentID, target, text string) (AgentInfo, error) {
	parent, err := m.owner(parentID)
	if err != nil {
		return AgentInfo{}, err
	}
	child, err := m.resolve(parent, target)
	if err != nil {
		return AgentInfo{}, err
	}
	if !m.r.hub.Running(child.Meta.SessionID) {
		return AgentInfo{}, fmt.Errorf("agent %q is %s, not running: use followup_task to give it new work",
			child.Meta.Subagent.Name, m.status(child.Meta.SessionID, child.Meta.Subagent.Status))
	}
	if _, err := m.r.hub.Steer(child.Meta.SessionID, "Message from your parent agent:\n"+text, text); err != nil {
		return AgentInfo{}, err
	}
	return m.info(child), nil
}

// interrupt stops a child's running turn and drops its queued work.
// The parent asked for it, so the end is not reported back.
func (m *subagentManager) interrupt(parentID, target string) (AgentInfo, bool, error) {
	parent, err := m.owner(parentID)
	if err != nil {
		return AgentInfo{}, false, err
	}
	child, err := m.resolve(parent, target)
	if err != nil {
		return AgentInfo{}, false, err
	}
	id := child.Meta.SessionID
	if !m.r.hub.Busy(id) {
		return m.info(child), false, nil
	}
	m.mu.Lock()
	m.quiet[id] = true
	m.mu.Unlock()
	m.r.hub.StopSession(id, "")
	m.r.hub.WaitIdle(id, 5*time.Second)
	info := m.info(child)
	if !finalStatus(info.Status) {
		info.Status = AgentInterrupted
	}
	return info, true, nil
}

// stopChildren quietly stops every in-flight child of parentID; it
// runs when the user stops the parent.
func (m *subagentManager) stopChildren(parentID string) {
	m.mu.Lock()
	var ids []string
	for id := range m.inflight {
		if m.parentOf[id] == parentID {
			m.quiet[id] = true
			ids = append(ids, id)
		}
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.r.hub.StopSession(id, "")
	}
}

// list reports the parent's children.
func (m *subagentManager) list(parentID string) ([]AgentInfo, error) {
	parent, err := m.owner(parentID)
	if err != nil {
		return nil, err
	}
	m.restore(parent)
	out := []AgentInfo{}
	for _, c := range m.children(parent) {
		out = append(out, m.info(c))
	}
	return out, nil
}

// onDone records a child's finished turn and wakes its parent; for a
// top-level session it delivers results that arrived as it finished.
func (m *subagentManager) onDone(info TurnInfo) {
	requeued := m.settleDelivery(info)
	child, err := m.load(info.Cwd, info.SessionID)
	if err != nil {
		return
	}
	if child.Meta.Subagent == nil {
		// A turn that failed to save keeps its results queued for the
		// next turn rather than waking one that may fail the same way.
		if !info.Stopped && !info.Cancelled && !requeued {
			m.wake(info.SessionID)
		}
		return
	}
	parentID := child.Meta.Subagent.ParentID
	if m.r.hub.Busy(info.SessionID) {
		// Another (follow-up) turn is queued; its end is the result.
		m.emit(parentID, m.info(child))
		return
	}
	status := AgentCompleted
	errMsg := ""
	switch {
	case info.Stopped || info.Cancelled:
		status = AgentInterrupted
		errMsg = "interrupted before finishing"
	case info.Error != "":
		status, errMsg = AgentFailed, info.Error
	}
	result := lastReply(child)
	if result == "" {
		result = strings.TrimSpace(info.Reply)
	}
	m.mu.Lock()
	quiet := m.quiet[info.SessionID]
	m.mu.Unlock()
	m.logged("record child end", m.record(info.SessionID, child.Meta.Cwd, outcome{
		Status: status, Result: result, Error: errMsg, Reported: quiet,
		CompletionID: info.SessionID + "#" + info.TurnID,
	}))
	// quiet stays set until the child gets new work: a stop that drops
	// a queued follow-up ends in two onDone calls, in either order.
	m.mu.Lock()
	delete(m.inflight, info.SessionID)
	if !quiet {
		m.addPendingLocked(parentID, info.SessionID)
	}
	m.mu.Unlock()
	if updated, err := m.load(child.Meta.Cwd, info.SessionID); err == nil {
		m.emit(parentID, m.info(updated))
	}
	if !quiet {
		m.wake(parentID)
	}
}

// lastReply is the child's final assistant text.
func lastReply(child transcript.Session) string {
	msgs, err := child.History()
	if err != nil {
		return ""
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == transcript.RoleUser && !msgs[i].IsNotify {
			break
		}
		if msgs[i].Role == transcript.RoleAssistant && strings.TrimSpace(msgs[i].Content) != "" {
			return strings.TrimSpace(msgs[i].Content)
		}
	}
	return ""
}
