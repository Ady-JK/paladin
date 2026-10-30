// subagent_mailbox.go delivers finished children's results to their
// parent exactly once: through wait_agent, a notice inside a running
// parent turn, or a wake turn for an idle parent. A result handed to a
// parent turn is reported only once that turn is saved; on restart the
// parent transcript is checked for completion ids already delivered.

package runtime

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/Ady-JK/paladin/core/internal/agent"
	"github.com/Ady-JK/paladin/core/internal/jsonx"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

// waitResult is wait_agent's outcome.
type waitResult struct {
	Agents   []AgentInfo
	TimedOut bool
}

// wait blocks until one of targets (all children when empty) has an
// undelivered result, none is still active, or timeout. While blocked
// the parent's hub slot is lent out so children can run.
func (m *subagentManager) wait(ctx context.Context, parentID string, targets []string, timeout time.Duration) (waitResult, error) {
	parent, err := m.owner(parentID)
	if err != nil {
		return waitResult{}, err
	}
	m.restore(parent)
	ids := []string{}
	if len(targets) == 0 {
		for _, c := range m.children(parent) {
			ids = append(ids, c.Meta.SessionID)
		}
	}
	for _, t := range targets {
		c, err := m.resolve(parent, t)
		if err != nil {
			return waitResult{}, err
		}
		if !slices.Contains(ids, c.Meta.SessionID) {
			ids = append(ids, c.Meta.SessionID)
		}
	}
	if len(ids) == 0 {
		return waitResult{Agents: []AgentInfo{}}, nil
	}
	restore := m.r.hub.Lend(parentID)
	defer restore()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		changed := m.changedChan(parentID)
		delivered := m.takePending(parentID, ids)
		active := false
		var infos []AgentInfo
		for _, id := range ids {
			child, err := m.load(parent.Meta.Cwd, id)
			if err != nil || child.Meta.Subagent == nil {
				continue
			}
			info := m.info(child)
			if !finalStatus(info.Status) {
				active = true
			}
			if !slices.Contains(delivered, id) {
				info.Result, info.CompletionID = "", ""
			}
			infos = append(infos, info)
		}
		if len(delivered) > 0 || !active {
			m.handOver(parentID, parent.Meta.Cwd, m.r.hub.RunningTurnID(parentID), delivered)
			return waitResult{Agents: infos}, nil
		}
		select {
		case <-changed:
		case <-deadline.C:
			return waitResult{Agents: infos, TimedOut: true}, nil
		case <-ctx.Done():
			return waitResult{}, errors.New("wait cancelled: the turn was stopped")
		}
	}
}

// wake starts a parent turn to deliver child results when the parent
// is idle. A busy parent takes them mid-turn or at its own end.
func (m *subagentManager) wake(parentID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	settings, known := m.last[parentID]
	if !known || len(m.pending[parentID]) == 0 || m.waking[parentID] || m.r.hub.Busy(parentID) {
		return
	}
	_, _, err := m.r.hub.Submit(agent.Request{
		SessionID: parentID, Cwd: settings.Cwd, Resume: true,
		Permissions: settings.Permissions, Provider: settings.Provider, Model: settings.Model,
	}, SubmitOpts{})
	if err == nil {
		m.waking[parentID] = true
	}
}

func (m *subagentManager) addPendingLocked(parentID, childID string) {
	if !slices.Contains(m.pending[parentID], childID) {
		m.pending[parentID] = append(m.pending[parentID], childID)
	}
}

func (m *subagentManager) dropPendingLocked(parentID, childID string) {
	m.pending[parentID] = slices.DeleteFunc(m.pending[parentID], func(id string) bool { return id == childID })
	if len(m.pending[parentID]) == 0 {
		delete(m.pending, parentID)
	}
}

// restore rebuilds a parent's mailbox from disk once per process:
// finished but unreported children, and children whose turn a restart
// cut off (recorded as interrupted).
func (m *subagentManager) restore(parent transcript.Session) {
	id := parent.Meta.SessionID
	m.mu.Lock()
	done := m.restored[id]
	m.restored[id] = true
	m.mu.Unlock()
	if done || len(parent.Meta.Children) == 0 {
		return
	}
	var unreported []transcript.Session
	for _, c := range m.children(parent) {
		sa := c.Meta.Subagent
		status := m.status(c.Meta.SessionID, sa.Status)
		if status == AgentInterrupted && !finalStatus(sa.Status) {
			// The lost turn's id is unknown; the stale state's time
			// names this result instead.
			sa.CompletionID = fmt.Sprintf("%s#lost-%d", c.Meta.SessionID, sa.UpdatedAt.UnixNano())
			sa.Status, sa.Reported = AgentInterrupted, false
			m.logged("record lost child turn", m.record(c.Meta.SessionID, c.Meta.Cwd, outcome{
				Status: AgentInterrupted, Result: sa.Result, Error: "interrupted: the app stopped while it was working",
				CompletionID: sa.CompletionID,
			}))
		}
		if finalStatus(status) && !sa.Reported {
			unreported = append(unreported, c)
		}
	}
	saved := m.alreadySaved(parent, unreported)
	var reported []string
	m.mu.Lock()
	for _, c := range unreported {
		if saved[c.Meta.Subagent.CompletionID] {
			reported = append(reported, c.Meta.SessionID)
			continue
		}
		m.addPendingLocked(id, c.Meta.SessionID)
	}
	m.mu.Unlock()
	m.logged("mark reconciled results reported", m.markReported(parent.Meta.Cwd, reported))
}

// alreadySaved finds which children's results the parent transcript
// already holds: a crash between saving the parent turn and marking the
// child reported must not deliver them twice.
func (m *subagentManager) alreadySaved(parent transcript.Session, children []transcript.Session) map[string]bool {
	var ids []string
	for _, c := range children {
		if cid := c.Meta.Subagent.CompletionID; cid != "" {
			ids = append(ids, cid)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	saved, err := parent.Mentions(ids)
	// Unsure means deliver again: a repeat beats a lost result.
	m.logged("look up delivered results", err)
	return saved
}

func (m *subagentManager) hasPending(parentID, cwd string) bool {
	if parent, err := m.load(cwd, parentID); err == nil {
		m.restore(parent)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.pending[parentID]) > 0
}

// takePending removes and returns the parent's pending children among
// only (all when nil).
func (m *subagentManager) takePending(parentID string, only []string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var taken []string
	for _, id := range m.pending[parentID] {
		if only == nil || slices.Contains(only, id) {
			taken = append(taken, id)
		}
	}
	for _, id := range taken {
		m.dropPendingLocked(parentID, id)
	}
	return taken
}

// handOver records results given to a parent turn. They are reported
// once that turn is saved; without a turn to tie them to, at once.
func (m *subagentManager) handOver(parentID, cwd, turnID string, ids []string) {
	if len(ids) == 0 {
		return
	}
	if turnID == "" {
		m.logged("mark results reported", m.markReported(cwd, ids))
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.delivering[turnID]
	d.parentID, d.cwd = parentID, cwd
	d.ids = append(d.ids, ids...)
	m.delivering[turnID] = d
}

// settleDelivery runs as a parent turn ends: what it took is reported
// when its transcript was saved, otherwise put back at the head of the
// mailbox. It reports whether anything was put back.
func (m *subagentManager) settleDelivery(info TurnInfo) bool {
	m.mu.Lock()
	d, ok := m.delivering[info.TurnID]
	delete(m.delivering, info.TurnID)
	if ok && !info.Persisted {
		rest := slices.DeleteFunc(m.pending[d.parentID], func(id string) bool { return slices.Contains(d.ids, id) })
		m.pending[d.parentID] = append(slices.Clone(d.ids), rest...)
	}
	m.mu.Unlock()
	if !ok {
		return false
	}
	if info.Persisted {
		m.logged("mark results reported", m.markReported(d.cwd, d.ids))
		return false
	}
	return true
}

// markReported records that the parent has the children's results. It
// tries every child and returns every failure.
func (m *subagentManager) markReported(cwd string, ids []string) error {
	var errs []error
	for _, id := range ids {
		err := m.r.transcripts.UpdateMeta(cwd, id, func(meta *transcript.Meta) error {
			if meta.Subagent != nil {
				meta.Subagent.Reported = true
			}
			return nil
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("child %s: %w", id, err))
		}
	}
	return errors.Join(errs...)
}

// notices is a parent turn's Request.Notices: finished children's
// results, as notify rows in the transcript.
func (m *subagentManager) notices(parentID, cwd string) func() []agent.Notice {
	return func() []agent.Notice {
		return m.deliverNotices(parentID, cwd, m.r.hub.RunningTurnID(parentID))
	}
}

// deliverNotices hands the parent's undelivered results to turnID.
func (m *subagentManager) deliverNotices(parentID, cwd, turnID string) []agent.Notice {
	if parent, err := m.load(cwd, parentID); err == nil {
		m.restore(parent)
	}
	ids := m.takePending(parentID, nil)
	var out []agent.Notice
	for _, id := range ids {
		child, err := m.load(cwd, id)
		if err != nil || child.Meta.Subagent == nil {
			continue
		}
		info := m.info(child)
		callID := info.CompletionID
		if callID == "" {
			// Recorded before completion ids existed.
			callID = fmt.Sprintf("%s#done-%d", id, info.UpdatedAt)
		}
		args, _ := jsonx.Marshal(map[string]string{"action": "notify", "agent_id": id, "name": info.Name})
		out = append(out, agent.Notice{
			CallID:    callID,
			Name:      "agent",
			Arguments: string(args),
			Content:   noticeContent(info),
			IsError:   info.Status == AgentFailed,
		})
	}
	m.handOver(parentID, cwd, turnID, ids)
	return out
}

func noticeContent(info AgentInfo) string {
	body := map[string]any{
		"event": "subagent_finished", "agent_id": info.ID, "name": info.Name, "type": info.Type,
		"status": info.Status,
		"note":   "The full transcript is in the child session. Use followup_task to give it more work.",
	}
	if info.Result != "" {
		body["result"] = clip(info.Result, resultLimit)
	}
	if info.Error != "" {
		body["error"] = info.Error
	}
	data, _ := jsonx.Marshal(body)
	return string(data)
}

func clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "\n…[truncated; read the child session for the rest]"
}
