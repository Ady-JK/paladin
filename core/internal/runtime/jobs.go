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
	// Auto marks a foreground command that outlived its 10s wait and was
	// moved to the background automatically; only those report every 5m.
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
	// stops ends each live job's five-minute notice loop.
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
	}
}

// JobBackgrounded marks a foreground command that outlived its wait.
// Only these jobs report their running time every five minutes.
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
	b.startNoticeLoop(rec.ID, rec.SessionID, rec.Workspace, rec.Command, rec.StartedAt)
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
	// A finished job's notice still has to reach an idle session.
	if wake {
		b.rt.wakeSession(sessionID, workspace)
	}
}

// jobNoticeEvery is how often an auto-backgrounded command reports that
// it is still running. A variable so tests can shorten it.
var jobNoticeEvery = 5 * time.Minute

func (b *jobBook) startNoticeLoop(id, sessionID, workspace, command string, started time.Time) {
	stop := make(chan struct{})
	b.mu.Lock()
	b.stops[id] = stop
	b.mu.Unlock()
	go func() {
		ticker := time.NewTicker(jobNoticeEvery)
		defer ticker.Stop()
		n := 0
		for {
			select {
			case <-ticker.C:
				b.mu.Lock()
				_, live := b.live[id]
				b.mu.Unlock()
				if !live {
					return
				}
				n++
				b.runningNotice(id, sessionID, workspace, command, time.Since(started), n)
			case <-stop:
				return
			}
		}
	}()
}

func (b *jobBook) runningNotice(id, sessionID, workspace, command string, elapsed time.Duration, n int) {
	body := map[string]any{
		"event": "job_running", "job_id": id, "command": command,
		"running_for": elapsed.Round(time.Second).String(),
		"note": "Still running in the background. Check it with the shell tool status action, " +
			"cancel it if it is no longer needed, or keep working on something that does not depend on it.",
	}
	data, err := jsonx.Marshal(body)
	if err != nil {
		return
	}
	args, _ := jsonx.Marshal(map[string]string{"action": "notify", "job_id": id})
	b.mu.Lock()
	b.notices[sessionID] = append(b.notices[sessionID], agent.Notice{
		CallID:    fmt.Sprintf("%s#running-%d", id, n),
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

// wakeSession starts a resume turn so an idle session can see a job
// notice. A busy session takes it at its next round instead.
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
