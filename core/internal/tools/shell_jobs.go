// shell_jobs.go tracks shell commands: start, status, cancel and the
// notices a running command sends.

package tools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Ady-JK/paladin/core/internal/sandbox"
	"github.com/Ady-JK/paladin/core/internal/syserr"
)

type job struct {
	id           string
	command      string
	log          string
	workspace    string
	sessionDir   string
	status       string
	exitCode     *int
	finished     bool
	notify       bool
	auto         bool
	killedByUser bool
	notifyCallID string
	owner        string
	reported     bool
	childPID     int
	logDone      bool
	startedAt    time.Time
	done         chan struct{}
	closed       bool
}

// broadcaster wakes all waiters; each notify swaps the channel.
type broadcaster struct {
	mu sync.Mutex
	ch chan struct{}
}

func newBroadcaster() *broadcaster {
	return &broadcaster{ch: make(chan struct{})}
}

func (b *broadcaster) notify() {
	b.mu.Lock()
	defer b.mu.Unlock()
	close(b.ch)
	b.ch = make(chan struct{})
}

type shellJobs struct {
	mu      sync.Mutex
	byID    map[string]*job
	counter atomic.Uint64
	changed *broadcaster
}

// JobEvent describes a shell command for undo and lifecycle bookkeeping.
type JobEvent struct {
	ID         string
	SessionID  string
	SessionDir string
	Workspace  string
	CallID     string
	Command    string
	StartedAt  time.Time
	EndedAt    time.Time
	Status     string
	ExitCode   *int
	// PID is the job's process (and process group) id; set from
	// JobSpawned on.
	PID int
	// Auto marks a command that outlived its foreground wait and was
	// moved to the background automatically.
	Auto bool
	// Candidate marks a foreground command that may still become an auto
	// job.
	Candidate bool
	// Notify asks for a completion report when the job ends.
	Notify bool
	// KilledByUser marks a job the user stopped from the app.
	KilledByUser bool
}

// JobObserver hears about shell commands. JobStarted runs inside the
// starting call, just before the process spawns; JobSpawned right after
// it spawned, with its PID; JobBackgrounded when a foreground command
// outlives its wait and becomes an auto job; JobFinished runs once per
// job after the process has exited (whether it finished, failed or was
// cancelled), or right away when it never started.
type JobObserver interface {
	JobStarted(JobEvent)
	JobSpawned(JobEvent)
	JobBackgrounded(JobEvent)
	JobFinished(JobEvent)
}

// Observe sets the job observer; call it before any job starts.
func (s *ShellTool) Observe(o JobObserver) { s.observer = o }

// startJob spawns one command with its output streamed to a log file
// and registers it. It never kills the command on a timer.
func (s *ShellTool) startJob(cmd, dir, owner, callID string, notify, candidate bool, sessionDir string, sb *sandbox.Policy) (*job, error) {
	if sessionDir == "" {
		return nil, ArgsError("shell: no session to keep the background job in")
	}
	now := time.Now().Unix()
	id := fmt.Sprintf("job-%d-%d", now, s.inner.counter.Add(1))
	tmp := filepath.Join(sessionDir, "tmp")
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return nil, ArgsError(fmt.Sprintf("shell: cannot keep the job log: %s", syserr.Str(err)))
	}
	logPath := filepath.Join(tmp, id+".log")
	logFile, err := os.Create(logPath)
	if err != nil {
		return nil, ArgsError(fmt.Sprintf("shell: cannot write the job log: %s", syserr.Str(err)))
	}
	started := time.Now().UTC()
	event := JobEvent{ID: id, SessionID: owner, SessionDir: sessionDir, Workspace: dir,
		CallID: callID, Command: cmd, StartedAt: started, Candidate: candidate, Notify: notify}
	if s.observer != nil {
		s.observer.JobStarted(event)
	}
	child, err := spawnToLog(cmd, dir, logFile, sb)
	logFile.Close()
	if err != nil {
		if s.observer != nil {
			one := 1
			event.EndedAt, event.Status, event.ExitCode = time.Now().UTC(), JobFailed, &one
			s.observer.JobFinished(event)
		}
		return nil, ArgsError(fmt.Sprintf("shell: cannot start the command: %s", syserr.Str(err)))
	}
	childPID := 0
	if child.Process != nil {
		childPID = child.Process.Pid
	}
	if s.observer != nil {
		event.PID = childPID
		s.observer.JobSpawned(event)
	}
	untrack := procTracker.Track(owner, childPID)
	j := &job{
		id: id, command: cmd, log: logPath, workspace: dir, sessionDir: sessionDir,
		status: JobRunning, notify: notify, notifyCallID: callID, owner: owner,
		childPID: childPID, startedAt: started, done: make(chan struct{}),
	}
	s.inner.mu.Lock()
	s.inner.byID[id] = j
	s.inner.mu.Unlock()
	go func() {
		err := child.Wait()
		// The job's shell is done; kill anything it left running in its
		// process group so no untracked daemon survives the job.
		killProcessGroup(childPID)
		untrack()
		s.finishJob(id, event, exitCodeOf(err))
	}()
	return j, nil
}

// runBackground starts a job and returns its id at once.
func (s *ShellTool) runBackground(cmd, dir, owner, callID string, notify bool, sessionDir string, sb *sandbox.Policy) (string, error) {
	j, err := s.startJob(cmd, dir, owner, callID, notify, false, sessionDir, sb)
	if err != nil {
		return "", err
	}
	return shellResponseJSON(j.id, JobRunning, nil, nil, nil, j.log, "", "")
}

// armAuto marks a still-running foreground command as an auto job: it
// now reports its completion and its running time. It returns false when
// the command finished in the meantime.
func (s *ShellTool) armAuto(id string) bool {
	s.inner.mu.Lock()
	j, ok := s.inner.byID[id]
	if !ok || j.finished {
		s.inner.mu.Unlock()
		return false
	}
	j.notify = true
	j.auto = true
	if j.notifyCallID == "" {
		j.notifyCallID = j.id
	}
	event := JobEvent{ID: j.id, SessionID: j.owner, SessionDir: j.sessionDir, Workspace: j.workspace,
		CallID: j.notifyCallID, Command: j.command, StartedAt: j.startedAt, PID: j.childPID, Auto: true}
	s.inner.mu.Unlock()
	if s.observer != nil {
		s.observer.JobBackgrounded(event)
	}
	return true
}

// markReported consumes a finished command's completion notice when its
// result already went back through the foreground call.
func (s *ShellTool) markReported(id string) {
	s.inner.mu.Lock()
	if j, ok := s.inner.byID[id]; ok {
		j.reported = true
	}
	s.inner.mu.Unlock()
}

// finishJob records a job's exit (unless it already ended, e.g. by
// cancel) and tells the observer and waiters.
func (s *ShellTool) finishJob(id string, event JobEvent, code *int) {
	s.inner.mu.Lock()
	final := event
	if j, ok := s.inner.byID[id]; ok {
		if !j.finished {
			j.finished = true
			switch {
			case code == nil:
				one := 1
				j.status, j.exitCode = JobFailed, &one
			case *code == 0:
				j.status, j.exitCode = JobDone, code
			default:
				j.status, j.exitCode = JobFailed, code
			}
		}
		j.logDone = true
		final.Status, final.ExitCode = j.status, j.exitCode
		final.KilledByUser = j.killedByUser
		j.closeLocked()
	}
	s.inner.mu.Unlock()
	// The process has exited, so the bookkeeping sees all its writes.
	if s.observer != nil {
		final.EndedAt = time.Now().UTC()
		s.observer.JobFinished(final)
	}
	s.inner.changed.notify()
}

func (j *job) closeLocked() {
	if !j.closed {
		close(j.done)
		j.closed = true
	}
}

// exitCodeOf maps a Wait error to an exit code; nil means the process
// did not exit normally (killed, or never waited on).
func exitCodeOf(err error) *int {
	if err == nil {
		zero := 0
		return &zero
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code := exitErr.ExitCode()
		return &code
	}
	return nil
}

// ownedJobLocked finds a job the caller's session started. The shell
// tool is shared by every session, so another session's job reads as
// unknown rather than revealing that it exists.
func (s *ShellTool) ownedJobLocked(jobID, caller string) (*job, bool) {
	j, ok := s.inner.byID[strings.TrimSpace(jobID)]
	if !ok || j.owner != caller {
		return nil, false
	}
	return j, true
}

func (s *ShellTool) status(jobID, caller string) (string, error) {
	s.inner.mu.Lock()
	j, ok := s.ownedJobLocked(jobID, caller)
	// Snapshot under the lock: the waiter goroutine mutates the entry
	// (status/exitCode/logDone) while a status check is in flight.
	var snap job
	if ok {
		snap = *j
	}
	s.inner.mu.Unlock()
	if !ok {
		return "", ArgsError(fmt.Sprintf("shell: unknown job %q", jobID))
	}
	text, err := shellResponseJSON(snap.id, snap.status, snap.exitCode, nil, nil, snap.log, "", "")
	if err != nil {
		return "", err
	}
	if snap.finished && snap.status == JobFailed {
		return "", WithContent(text, fmt.Sprintf("shell: job %s %s", snap.id, snap.status))
	}
	return text, nil
}

// waitSeconds clamps the wait action's block time: the default when the
// caller gave none, and the cap so one call cannot stall a turn for longer.
func waitSeconds(n int) int {
	if n <= 0 {
		return DefaultShellWaitSeconds
	}
	return min(n, MaxShellWaitSeconds)
}

// wait blocks until the job ends or the caller's deadline passes, so the
// model can take a finished result without polling with a sleep. A job
// this call saw finish is reported here, not again through the notice
// stream.
func (s *ShellTool) wait(jobID string, seconds int, caller string, cancel context.Context) (string, error) {
	if strings.TrimSpace(jobID) == "" {
		return "", ArgsError("shell: job_id is required for this action")
	}
	seconds = waitSeconds(seconds)
	s.inner.mu.Lock()
	j, ok := s.ownedJobLocked(jobID, caller)
	if !ok {
		s.inner.mu.Unlock()
		return "", ArgsError(fmt.Sprintf("shell: unknown job %q", jobID))
	}
	done := j.done
	s.inner.mu.Unlock()
	if cancel == nil {
		cancel = context.Background()
	}
	timer := time.NewTimer(time.Duration(seconds) * time.Second)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		return s.runningResult(jobID, fmt.Sprintf(
			"still running after %ds; call wait again if you need the result now, or keep working and let its completion notice arrive.", seconds))
	case <-cancel.Done():
		return "", ArgsError("shell: the wait was interrupted")
	}
	// The result this call returns is the completion report, so consume it.
	s.markReported(jobID)
	return s.completed(jobID, false)
}

func (s *ShellTool) cancel(jobID, caller string) (string, error) {
	return s.killJob(jobID, func(j *job) bool { return j.owner == caller }, false)
}

// killJob stops a job. match selects the jobs the caller may stop;
// byUser records that the user did it from the app.
func (s *ShellTool) killJob(jobID string, match func(*job) bool, byUser bool) (string, error) {
	s.inner.mu.Lock()
	j, ok := s.inner.byID[strings.TrimSpace(jobID)]
	if !ok || !match(j) {
		s.inner.mu.Unlock()
		return "", ArgsError(fmt.Sprintf("shell: unknown job %q", jobID))
	}
	pid := 0
	if !j.finished {
		j.finished = true
		j.status = JobCancelled
		j.killedByUser = byUser
		pid = j.childPID
		j.closeLocked()
	}
	snap := *j
	s.inner.mu.Unlock()
	if pid != 0 {
		killProcessGroup(pid)
	}
	s.inner.changed.notify()
	return shellResponseJSON(snap.id, snap.status, snap.exitCode, nil, nil, snap.log, "", "")
}

type NotifyResult struct {
	CallID       string
	JobID        string
	Status       string
	ExitCode     *int
	Output       string
	Truncated    bool
	IsError      bool
	KilledByUser bool
}

// JobInfo is one running command, as the app's running-jobs panel sees it.
type JobInfo struct {
	ID        string    `json:"id"`
	SessionID string    `json:"sessionId"`
	Workspace string    `json:"workspace,omitempty"`
	Command   string    `json:"command"`
	StartedAt time.Time `json:"startedAt"`
	ElapsedMs int64     `json:"elapsedMs"`
	Status    string    `json:"status"`
	Auto      bool      `json:"auto,omitempty"`
}

// RunningJobs lists every command still running, oldest first.
func (s *ShellTool) RunningJobs() []JobInfo {
	now := time.Now()
	s.inner.mu.Lock()
	out := make([]JobInfo, 0, len(s.inner.byID))
	for _, j := range s.inner.byID {
		if j.finished {
			continue
		}
		out = append(out, infoOf(j, now))
	}
	s.inner.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	return out
}

func infoOf(j *job, now time.Time) JobInfo {
	elapsed := now.Sub(j.startedAt)
	if elapsed < 0 {
		elapsed = 0
	}
	return JobInfo{ID: j.id, SessionID: j.owner, Workspace: j.workspace, Command: j.command,
		StartedAt: j.startedAt, ElapsedMs: elapsed.Milliseconds(), Status: j.status, Auto: j.auto}
}

// KillJob stops any running command on the user's behalf and makes sure
// its completion is reported, so a later turn knows the user stopped it.
func (s *ShellTool) KillJob(id string) (JobInfo, error) {
	s.inner.mu.Lock()
	j, ok := s.inner.byID[strings.TrimSpace(id)]
	if !ok || j.finished {
		s.inner.mu.Unlock()
		return JobInfo{}, ArgsError(fmt.Sprintf("shell: unknown job %q", id))
	}
	j.finished = true
	j.status = JobCancelled
	j.killedByUser = true
	j.notify = true
	pid := j.childPID
	info := infoOf(j, time.Now())
	j.closeLocked()
	s.inner.mu.Unlock()
	if pid != 0 {
		killProcessGroup(pid)
	}
	s.inner.changed.notify()
	return info, nil
}

func (s *ShellTool) PollNotify(owner string) []NotifyResult {
	s.inner.mu.Lock()
	snapshot := make([]*job, 0, len(s.inner.byID))
	for _, j := range s.inner.byID {
		cp := *j
		snapshot = append(snapshot, &cp)
	}
	s.inner.mu.Unlock()
	var out []NotifyResult
	for _, js := range snapshot {
		s.inner.mu.Lock()
		entry, ok := s.inner.byID[js.id]
		if !ok {
			s.inner.mu.Unlock()
			continue
		}
		if !entry.notify || entry.owner != owner || !entry.finished || entry.reported || !entry.logDone {
			s.inner.mu.Unlock()
			continue
		}
		entry.reported = true
		status, exitCode, callID, jobID, log := entry.status, entry.exitCode, entry.notifyCallID, entry.id, entry.log
		killedByUser := entry.killedByUser
		s.inner.mu.Unlock()
		tail, truncated := tailFile(log)
		out = append(out, NotifyResult{
			CallID: callID, JobID: jobID, Status: status, ExitCode: exitCode,
			Output: tail, Truncated: truncated,
			IsError: status == JobFailed, KilledByUser: killedByUser,
		})
	}
	return out
}

// RunningPeekLines is how much of a still-running command's output a
// running answer carries: enough for the model to see what the command
// is doing, without pulling a multi-megabyte log into the turn.
const (
	RunningPeekLines = 5
	// RunningPeekBytes bounds the read that produces that peek.
	RunningPeekBytes = 8 * 1024
)

// peekTail returns the last RunningPeekLines lines of a log. It reads a bounded
// window from the end rather than the whole file, because a running
// command's log can be arbitrarily large and this is only a status peek.
// truncated reports that the peek dropped earlier output.
func peekTail(path string) (lines []string, truncated bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.Size() == 0 {
		return nil, false
	}
	size := st.Size()
	window := int64(RunningPeekBytes)
	partial := size > window
	if !partial {
		window = size
	}
	if _, err := f.Seek(size-window, io.SeekStart); err != nil {
		return nil, false
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, false
	}
	slice := data
	if partial {
		// The window starts mid-line; drop that fragment so the first
		// line shown is a whole one.
		if i := indexByte(slice, '\n'); i >= 0 {
			slice = slice[i+1:]
		} else {
			return nil, true
		}
	}
	text := ToLF(strings.ToValidUTF8(string(slice), "�"))
	all := strings.Split(text, "\n")
	// A log that ends in a newline has an empty trailing element.
	for len(all) > 0 && all[len(all)-1] == "" {
		all = all[:len(all)-1]
	}
	if len(all) > RunningPeekLines {
		all = all[len(all)-RunningPeekLines:]
		truncated = true
	}
	return all, truncated
}

// tailFile returns the last bytes of a log file without reading the
// whole thing.
func tailFile(path string) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", false
	}
	size := st.Size()
	truncated := size > MaxNotifyOutput
	if truncated {
		if _, err := f.Seek(size-MaxNotifyOutput, io.SeekStart); err != nil {
			return "", false
		}
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return "", false
	}
	slice := data
	if truncated {
		if i := indexByte(slice, '\n'); i >= 0 {
			slice = slice[i+1:]
		} else {
			slice = nil
		}
	}
	text := ToLF(string(slice))
	lines := strings.Split(text, "\n")
	if len(lines) > NotifyTailLines {
		lines = lines[len(lines)-NotifyTailLines:]
		truncated = true
	}
	return strings.Join(lines, "\n"), truncated
}
