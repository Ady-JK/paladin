// subagent_mailbox.go delivers finished children's results to their
// parent exactly once: through wait, a notice inside a running
// parent turn, or a wake turn for an idle parent. A result handed to a
// parent turn is reported only once that turn is saved; on restart the
// parent transcript is checked for completion ids already delivered.

package runtime

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Ady-JK/paladin/core/internal/agent"
	"github.com/Ady-JK/paladin/core/internal/jsonx"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

// waitResult is wait's outcome. Jobs are shell commands that finished
// while the call was blocked, batched with whatever the named target
// produced.
type waitResult struct {
	Agents   []AgentInfo
	Jobs     []map[string]any
	TimedOut bool
}

// wait blocks until the named target (a child by id or name, or a shell
// job by id) finishes, or — with no target — until any child or running
// job of the session finishes. Whatever else completes while the call is
// blocked is delivered in the same answer, so the results arrive as one
// batch. While blocked the parent's hub slot is lent out so children can
// run. On timeout or cancellation nothing is consumed and nothing killed.
func (m *subagentManager) wait(ctx context.Context, parentID, target string, timeout time.Duration) (waitResult, error) {
	parent, err := m.owner(parentID)
	if err != nil {
		return waitResult{}, err
	}
	m.restore(parent)

	// Resolve the target first: it names a child (id or name) or a shell
	// job of this session, and anything else is an error the model can fix.
	targetChild, targetJob := "", ""
	if t := strings.TrimSpace(target); t != "" {
		if c, err := m.resolve(parent, t); err == nil {
			targetChild = c.Meta.SessionID
		} else if m.r.jobs.runningJob(parentID, t) {
			targetJob = t
		} else {
			return waitResult{}, fmt.Errorf("unknown target %q: no subagent or running job of this session matches", t)
		}
	}

	// Nothing to wait for is not an error, but it must not hold the turn
	// open either: say so and let the model get on with it.
	if targetChild == "" && targetJob == "" && len(m.children(parent)) == 0 && len(m.r.jobs.running(parentID)) == 0 {
		return waitResult{Agents: []AgentInfo{}, Jobs: []map[string]any{}}, nil
	}

	restore := m.r.hub.Lend(parentID)
	defer restore()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	// What a pass takes from the mailbox is gone for good, so a pass that
	// does not end the wait carries it into the next one.
	var carried []string
	var carriedJobs []map[string]any
	for {
		changed := m.changedChan(parentID)
		jobChanged := m.r.jobs.waitChan(parentID)
		delivered := slices.Concat(carried, m.takePending(parentID, nil))

		// Child half: the target (or every child with no target), plus any
		// other child that finished meanwhile — its undelivered result
		// rides along as part of the batch.
		var infos []AgentInfo
		targetDone, anyDone := false, false
		for _, c := range m.children(parent) {
			id := c.Meta.SessionID
			if c.Meta.Subagent == nil {
				continue
			}
			info := m.info(c)
			finished := finalStatus(info.Status)
			if targetChild != "" && id != targetChild && !finished {
				continue // not the target and still working: not part of the answer
			}
			if !slices.Contains(delivered, id) {
				info.Result, info.CompletionID = "", ""
			}
			if finished && !slices.Contains(delivered, id) {
				anyDone = true
			}
			if id == targetChild && finished {
				targetDone = true
			}
			info.ID = m.modelID(parentID, id)
			infos = append(infos, info)
		}

		// Job half: completions the shell still owes this session, taken
		// from its notify accounting so a finished job is never reported
		// twice. The wait consumes them exactly as a round would.
		jobs := slices.Concat(carriedJobs, m.takeJobResults(parentID))
		targetJobDone := targetJob == "" || !m.r.jobs.runningJob(parentID, targetJob)
		if targetJob != "" && targetJobDone && !jobInList(jobs, targetJob) {
			if row := m.jobRow(parent, targetJob); row != nil {
				jobs = append(jobs, row)
			}
		}

		// A delivered result or a finished-job notice ends the wait; the
		// named target's completion is what decides it when there is one.
		done := len(delivered) > 0 || anyDone || len(jobs) > 0
		if targetChild != "" {
			done = targetDone
		}
		if targetJob != "" {
			done = targetJobDone
		}
		if done {
			m.handOver(parentID, parent.Meta.Cwd, m.r.hub.RunningTurnID(parentID), delivered)
			return waitResult{Agents: infos, Jobs: jobs}, nil
		}
		carried, carriedJobs = delivered, jobs
		select {
		case <-changed:
		case <-jobChanged:
		case <-deadline.C:
			// The answer carries what was taken, so it counts as delivered.
			m.handOver(parentID, parent.Meta.Cwd, m.r.hub.RunningTurnID(parentID), delivered)
			return waitResult{Agents: infos, Jobs: jobs, TimedOut: true}, nil
		case <-ctx.Done():
			// Nothing is consumed on cancellation: the children's results
			// go back to the mailbox for the next wait or round.
			m.mu.Lock()
			for _, id := range delivered {
				m.addPendingLocked(parentID, id)
			}
			m.mu.Unlock()
			return waitResult{}, errors.New("wait cancelled: the turn was stopped")
		}
	}
}

// takeJobResults consumes the session's finished-job notices that are
// due and shapes them as wait's answer rows: exit code and a bounded tail
// of the log, never the whole file. PollNotify marks them reported, so
// neither a later round nor a wake turn repeats them.
func (m *subagentManager) takeJobResults(sessionID string) []map[string]any {
	if m.r.shell == nil {
		return nil
	}
	var out []map[string]any
	for _, n := range m.r.shell.PollNotify(sessionID) {
		row := map[string]any{"job_id": n.JobID, "status": n.Status}
		if n.ExitCode != nil {
			row["exit_code"] = *n.ExitCode
		}
		if n.KilledByUser {
			row["killed_by_user"] = true
		}
		if n.Output != "" {
			row["output"] = clip(n.Output, resultLimit)
		}
		out = append(out, row)
	}
	return out
}

// jobRow is one finished job the records know about but the shell's
// pending notices do not (e.g. a notify:false job a wait named as its
// target): exit code and status without the notify accounting.
func (m *subagentManager) jobRow(parent transcript.Session, jobID string) map[string]any {
	for _, rec := range readJobRecords(parent.Dir) {
		if rec.ID == jobID {
			row := map[string]any{"job_id": rec.ID, "status": rec.Status, "command": firstCommandLine(rec.Command)}
			if rec.ExitCode != nil {
				row["exit_code"] = *rec.ExitCode
			}
			if rec.KilledByUser {
				row["killed_by_user"] = true
			}
			return row
		}
	}
	return nil
}

func jobInList(jobs []map[string]any, id string) bool {
	for _, j := range jobs {
		if j["job_id"] == id {
			return true
		}
	}
	return false
}

func firstCommandLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
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
		modelID := m.modelID(parentID, id)
		args, _ := jsonx.Marshal(map[string]string{"action": "notify", "agent_id": modelID, "name": info.Name})
		info.ID = modelID
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
