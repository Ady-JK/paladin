// jobs.go keeps the runtime's view of shell commands: which ones are
// still running, the notices they send, and waking an idle session for
// them. Undo is per turn now, so a job no longer takes its own snapshot.

package runtime

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Ady-JK/paladin/core/internal/agent"
	"github.com/Ady-JK/paladin/core/internal/atomicfile"
	"github.com/Ady-JK/paladin/core/internal/jsonx"
	"github.com/Ady-JK/paladin/core/internal/tools"
)

const jobsDir = "jobs"

// JobInterrupted marks a job whose sidecar exited before the job did.
const JobInterrupted = "interrupted"

// JobRecord is a background job's persisted lifecycle record.
type JobRecord struct {
	ID        string    `json:"id"`
	SessionID string    `json:"sessionId"`
	Workspace string    `json:"workspace"`
	CallID    string    `json:"callId"`
	Turn      int       `json:"turn"`
	StartedAt time.Time `json:"startedAt"`
	EndedAt   time.Time `json:"endedAt,omitempty"`
	Status    string    `json:"status"`
	ExitCode  *int      `json:"exitCode,omitempty"`
	// Command is the shell command line, shown in the running-jobs panel.
	Command string `json:"command,omitempty"`
	// Auto marks a foreground command that outlived its foreground wait
	// and was moved to the background automatically.
	Auto bool `json:"auto,omitempty"`
	// Candidate marks a foreground command that may still become an auto
	// job. It is not a tracked job until it backgrounds.
	Candidate bool `json:"-"`
	// Notify asks for a completion report and a wake when the session
	// is idle.
	Notify bool `json:"notify,omitempty"`
	// KilledByUser marks a job the user stopped from the panel.
	KilledByUser bool `json:"killedByUser,omitempty"`
	// PID is the job's process group, used to tell whether a job left
	// running by an earlier sidecar process is still alive.
	PID       int  `json:"pid,omitempty"`
	Finalized bool `json:"finalized"`

	dir string
}

type jobBook struct {
	rt *AgentRuntime

	mu         sync.Mutex
	live       map[string]*JobRecord
	candidates map[string]*JobRecord
	// notices are running-job reminders waiting for the session's next
	// round, or for the wake turn that delivers them.
	notices map[string][]agent.Notice
	// stops ends each live job's log monitor.
	stops map[string]chan struct{}
}

func newJobBook(rt *AgentRuntime) *jobBook {
	return &jobBook{rt: rt, live: map[string]*JobRecord{}, candidates: map[string]*JobRecord{},
		notices: map[string][]agent.Notice{}, stops: map[string]chan struct{}{}}
}

// running lists a session's jobs that are still running.
func (b *jobBook) running(sessionID string) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var ids []string
	for id, rec := range b.live {
		if rec.SessionID == sessionID {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func (b *jobBook) JobStarted(e tools.JobEvent) {
	rec := &JobRecord{ID: e.ID, SessionID: e.SessionID, Workspace: e.Workspace, CallID: e.CallID,
		Command: e.Command, StartedAt: e.StartedAt, Status: tools.JobRunning, dir: e.SessionDir,
		Turn: -1, Candidate: e.Candidate, Notify: e.Notify}
	b.mu.Lock()
	if e.Candidate {
		b.candidates[e.ID] = rec
		b.mu.Unlock()
		return
	}
	b.live[e.ID] = rec
	b.mu.Unlock()
	if err := writeJobRecord(rec); err != nil {
		log.Printf("jobs: record %s: %v", e.ID, err)
	}
}

func (b *jobBook) JobSpawned(e tools.JobEvent) {
	b.mu.Lock()
	rec := b.live[e.ID]
	if rec == nil {
		rec = b.candidates[e.ID]
	}
	if rec != nil {
		rec.PID = e.PID
	}
	b.mu.Unlock()
	if rec != nil && !rec.Candidate {
		if err := writeJobRecord(rec); err != nil {
			log.Printf("jobs: record %s pid: %v", e.ID, err)
		}
		// An explicit background job that asked to be told is watched from
		// the moment it starts; an auto job is watched when it backgrounds.
		if rec.Notify {
			b.startMonitor(rec)
		}
	}
}

// JobBackgrounded marks a foreground command that outlived its wait. It is
// watched from here on, like a background job that asked to be told.
func (b *jobBook) JobBackgrounded(e tools.JobEvent) {
	b.mu.Lock()
	rec := b.candidates[e.ID]
	delete(b.candidates, e.ID)
	if rec == nil {
		rec = b.live[e.ID]
	}
	if rec == nil {
		b.mu.Unlock()
		return
	}
	rec.Candidate = false
	rec.Auto = true
	rec.PID = e.PID
	b.live[e.ID] = rec
	b.mu.Unlock()
	if err := writeJobRecord(rec); err != nil {
		log.Printf("jobs: record %s background: %v", e.ID, err)
	}
	b.startMonitor(rec)
}

func (b *jobBook) JobFinished(e tools.JobEvent) {
	b.mu.Lock()
	rec := b.live[e.ID]
	if rec == nil {
		// A foreground command that finished inside its wait: it never
		// was a job, so it leaves no record behind.
		delete(b.candidates, e.ID)
		b.mu.Unlock()
		return
	}
	if stop := b.stops[e.ID]; stop != nil {
		close(stop)
		delete(b.stops, e.ID)
	}
	b.mu.Unlock()
	if rec.Finalized {
		return
	}
	rec.Status, rec.ExitCode, rec.EndedAt = e.Status, e.ExitCode, e.EndedAt
	rec.KilledByUser = e.KilledByUser
	wake := rec.Auto || rec.Notify || e.KilledByUser
	sessionID, workspace := rec.SessionID, rec.Workspace
	b.mu.Lock()
	rec.Finalized = true
	delete(b.live, e.ID)
	b.mu.Unlock()
	if err := writeJobRecord(rec); err != nil {
		log.Printf("jobs: finalize %s: %v", e.ID, err)
	}
	// The card's own result is the snapshot taken when the command detached,
	// so it would read "running" forever: tell the app the job is gone and
	// let it read the session again.
	b.rt.hub.PublishSession(sessionID, "session/refresh")
	// A finished job's notice still has to reach an idle session.
	if wake {
		b.rt.wakeSession(sessionID, workspace)
	}
}

// How a background job is watched. The monitor samples the job's log size
// every jobCheckEvery (cheap: a stat, no model call) and wakes the agent only
// for a reason: an hourly note while the log keeps growing, or a stall note
// when it has gone quiet. Variables so tests can shorten them.
var (
	jobCheckEvery    = 30 * time.Second
	jobProgressEvery = time.Hour
	jobStallAfter    = 10 * time.Minute
	jobStuckAfter    = 30 * time.Minute
)

const (
	noticeProgress = "job_progress"
	noticeStalled  = "job_stalled"
	noticeStuck    = "job_stuck"
)

// logWatch decides which notice a job is due from the size of its log. It
// holds no timer: the caller feeds it the clock and the size.
//
//	stage 0  printing, or quiet for less than jobStallAfter
//	stage 1  stall notice sent; a second one is due at jobStuckAfter
//	stage 2  presumed-stuck notice sent; nothing more until it prints again
//
// Any new output sends it back to stage 0, so a job that wakes up gets the
// full silent window again.
type logWatch struct {
	size     int64
	grewAt   time.Time
	noticeAt time.Time
	stage    int
}

func newLogWatch(now time.Time, size int64) *logWatch {
	return &logWatch{size: size, grewAt: now, noticeAt: now}
}

// step records the log's size at now and returns the notice that is due, or
// the empty string. It also reports how long the log has been silent.
func (w *logWatch) step(now time.Time, size int64) (kind string, silent time.Duration) {
	if size > w.size {
		w.grewAt, w.stage = now, 0
	}
	w.size = size
	silent = now.Sub(w.grewAt)
	switch {
	case w.stage == 0 && silent >= jobStallAfter:
		w.stage, w.noticeAt = 1, now
		return noticeStalled, silent
	case w.stage == 1 && silent >= jobStuckAfter:
		w.stage, w.noticeAt = 2, now
		return noticeStuck, silent
	case w.stage == 0 && now.Sub(w.noticeAt) >= jobProgressEvery:
		w.noticeAt = now
		return noticeProgress, silent
	}
	return "", silent
}

// startMonitor watches a background job until it ends. Auto-backgrounded jobs
// and background jobs that asked to be told (notify, the default) are
// watched; a job started with notify false is not.
func (b *jobBook) startMonitor(rec *JobRecord) {
	id, sessionID, workspace, command, started := rec.ID, rec.SessionID, rec.Workspace, rec.Command, rec.StartedAt
	logPath := tools.JobLogPath(rec.dir, id)
	b.mu.Lock()
	if b.stops[id] != nil {
		b.mu.Unlock()
		return
	}
	stop := make(chan struct{})
	b.stops[id] = stop
	b.mu.Unlock()
	go func() {
		ticker := time.NewTicker(jobCheckEvery)
		defer ticker.Stop()
		watch := newLogWatch(time.Now(), tools.LogSize(logPath))
		n := 0
		for {
			select {
			case <-ticker.C:
				b.mu.Lock()
				_, live := b.live[id]
				if !live {
					delete(b.stops, id)
				}
				b.mu.Unlock()
				if !live {
					return
				}
				now := time.Now()
				kind, silent := watch.step(now, tools.LogSize(logPath))
				if kind == "" {
					continue
				}
				n++
				b.jobNotice(kind, id, sessionID, workspace, command, logPath, now.Sub(started), silent, n)
			case <-stop:
				return
			}
		}
	}()
}

// jobNotice tells the agent about a job that is still running, with the tail
// of its log so it can judge whether the job is working or stuck. It wakes an
// idle session; a busy one reads it before its next model call.
func (b *jobBook) jobNotice(kind, id, sessionID, workspace, command, logPath string, running, silent time.Duration, n int) {
	last := tools.LogTail(logPath)
	if last == "" {
		last = "(no output yet)"
	}
	body := map[string]any{
		"event": kind, "job_id": id, "command": command,
		"running_for": running.Round(time.Second).String(),
		"last_output": last,
	}
	switch kind {
	case noticeProgress:
		body["note"] = "Still running and still printing. Nothing is needed from you: keep working on something that does not " +
			"depend on it, or end your turn; you are woken when it finishes. Cancel it with the shell tool's cancel action only if it is no longer needed."
	case noticeStalled:
		body["silent_for"] = silent.Round(time.Second).String()
		body["note"] = "No output for this long. It may be stuck, or doing quiet work. Judge from last_output: cancel it with the " +
			"shell tool's cancel action if it looks hung, or leave it running. You are woken when it finishes, and once more if it stays silent."
	case noticeStuck:
		body["silent_for"] = silent.Round(time.Second).String()
		body["note"] = "Presumed stuck: no output for a long time. This is the last silence notice for this job unless it prints again, and it " +
			"has not been stopped. Cancel it with the shell tool's cancel action, or leave it running."
	}
	data, err := jsonx.Marshal(body)
	if err != nil {
		return
	}
	args, _ := jsonx.Marshal(map[string]string{"action": "notify", "job_id": id})
	b.mu.Lock()
	b.notices[sessionID] = append(b.notices[sessionID], agent.Notice{
		CallID:    fmt.Sprintf("%s#%s-%d", id, kind, n),
		Name:      "shell",
		Arguments: string(args),
		Content:   string(data),
	})
	b.mu.Unlock()
	b.rt.wakeSession(sessionID, workspace)
}

// withNotices chains running-job notices in front of next, so one
// callable answers the agent's Notices hook.
func (b *jobBook) withNotices(sessionID string, next func() []agent.Notice) func() []agent.Notice {
	return func() []agent.Notice {
		b.mu.Lock()
		out := b.notices[sessionID]
		delete(b.notices, sessionID)
		b.mu.Unlock()
		if next != nil {
			out = append(out, next()...)
		}
		return out
	}
}

// RunningJobs lists every shell command still running, for the app's
// running-jobs panel.
func (r *AgentRuntime) RunningJobs() []tools.JobInfo { return r.shell.RunningJobs() }

// KillJob stops a running command on the user's behalf. The completion
// notice tells the agent who stopped it.
func (r *AgentRuntime) KillJob(id string) (tools.JobInfo, error) { return r.shell.KillJob(id) }

// afterTurn wakes a session whose turn ended with a job notice still queued.
// A notice that arrives while the session is busy is read at its next round,
// but one that lands after the turn's last round would otherwise wait for an
// unrelated event, and with no wait action nothing else would wake the agent.
// Only a turn that finished normally is resumed: a stopped or failed turn is
// the user's call.
func (b *jobBook) afterTurn(info TurnInfo) {
	if info.SessionID == "" || info.State != TurnDone || info.Stopped || info.Cancelled || info.Error != "" {
		return
	}
	b.mu.Lock()
	pending := len(b.notices[info.SessionID]) > 0
	b.mu.Unlock()
	if pending || b.rt.shell.HasNotify(info.SessionID) {
		b.rt.wakeSession(info.SessionID, info.Cwd)
	}
}

// wakeSession starts a resume turn so an idle session can see a job
// notice. A busy session takes it at its next round instead; afterTurn
// covers the notice that lands after that session's last round.
func (r *AgentRuntime) wakeSession(sessionID, cwd string) {
	if sessionID == "" || r.hub.Busy(sessionID) {
		return
	}
	s := r.subagents.settingsFor(sessionID, cwd)
	if s.Cwd == "" {
		s.Cwd = cwd
	}
	if _, _, err := r.hub.Submit(agent.Request{
		SessionID: sessionID, Cwd: s.Cwd, Resume: true,
		Permissions: s.Permissions, Provider: s.Provider, Model: s.Model,
	}, SubmitOpts{}); err != nil && !strings.Contains(err.Error(), "shutting down") {
		log.Printf("jobs: wake %s: %v", sessionID, err)
	}
}

func writeJobRecord(rec *JobRecord) error {
	if rec.dir == "" {
		return nil
	}
	dir := filepath.Join(rec.dir, jobsDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := jsonx.MarshalIndent(rec)
	if err != nil {
		return err
	}
	return atomicfile.Write(filepath.Join(dir, rec.ID+".json"), data)
}

// readJobRecords loads a session's job records; unreadable ones are
// skipped.
func readJobRecords(sessionDir string) []JobRecord {
	entries, err := os.ReadDir(filepath.Join(sessionDir, jobsDir))
	if err != nil {
		return nil
	}
	var out []JobRecord
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(sessionDir, jobsDir, e.Name()))
		if err != nil {
			continue
		}
		var rec JobRecord
		if json.Unmarshal(data, &rec) == nil {
			out = append(out, rec)
		}
	}
	return out
}

// orphans are the session's job records an earlier sidecar process
// left unfinished: not finalized and not running in this one.
func (b *jobBook) orphans(sessionDir string) []JobRecord {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []JobRecord
	for _, rec := range readJobRecords(sessionDir) {
		if _, live := b.live[rec.ID]; !live && !rec.Finalized {
			rec.dir = sessionDir
			out = append(out, rec)
		}
	}
	return out
}

// checkIdle refuses a revert or redo while any of the session's jobs
// could still be writing: one running here, or one an earlier sidecar
// left behind whose process group is still alive.
func (b *jobBook) checkIdle(sessionID, sessionDir string) error {
	if ids := b.running(sessionID); len(ids) > 0 {
		return errJobsRunning(ids)
	}
	var alive []string
	for _, rec := range b.orphans(sessionDir) {
		if groupAlive(rec.PID) {
			alive = append(alive, fmt.Sprintf("%s (pid %d)", rec.ID, rec.PID))
		}
	}
	if len(alive) > 0 {
		return fmt.Errorf("background job %s from an earlier run of Paladin is still running; stop it or wait for it to exit, then retry", strings.Join(alive, ", "))
	}
	return nil
}

// errJobsRunning refuses a revert while the session's jobs could still
// be writing.
func errJobsRunning(ids []string) error {
	return fmt.Errorf("background job %s is still running; cancel it or wait for it to finish, then retry", strings.Join(ids, ", "))
}
