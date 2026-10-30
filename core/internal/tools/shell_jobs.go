// shell_jobs.go tracks background shell jobs: start, timeout, status,
// cancel and completion notices.

package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Ady-JK/paladin/core/internal/jsonx"
	"github.com/Ady-JK/paladin/core/internal/sandbox"
	"github.com/Ady-JK/paladin/core/internal/syserr"
)

type job struct {
	id           string
	log          string
	status       string
	exitCode     *int
	finished     bool
	notify       bool
	notifyCallID string
	owner        string
	reported     bool
	childPID     int
	logDone      bool
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

func (b *broadcaster) waitChan() <-chan struct{} {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.ch
}

type shellJobs struct {
	mu      sync.Mutex
	byID    map[string]*job
	counter atomic.Uint64
	changed *broadcaster
}

// JobEvent describes a background job for undo bookkeeping.
type JobEvent struct {
	ID         string
	SessionID  string
	SessionDir string
	Workspace  string
	CallID     string
	StartedAt  time.Time
	EndedAt    time.Time
	Status     string
	ExitCode   *int
	// PID is the job's process (and process group) id; set from
	// JobSpawned on.
	PID int
}

// JobObserver hears about background jobs. JobStarted runs inside the
// starting call, just before the process spawns; JobSpawned right after
// it spawned, with its PID; JobFinished runs once
// per job after the process has exited (whether it finished, failed,
// was cancelled or timed out), or right away when it never started.
type JobObserver interface {
	JobStarted(JobEvent)
	JobSpawned(JobEvent)
	JobFinished(JobEvent)
}

// Observe sets the job observer; call it before any job starts.
func (s *ShellTool) Observe(o JobObserver) { s.observer = o }

func (s *ShellTool) runBackground(cmd, dir, owner, callID string, limit time.Duration, notify bool, sessionDir string, sb *sandbox.Policy) (string, error) {
	if sessionDir == "" {
		return "", ArgsError("shell: no session to keep the background job in")
	}
	now := time.Now().Unix()
	id := fmt.Sprintf("job-%d-%d", now, s.inner.counter.Add(1))
	tmp := filepath.Join(sessionDir, "tmp")
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return "", ArgsError(fmt.Sprintf("shell: cannot keep the job log: %s", syserr.Str(err)))
	}
	logPath := filepath.Join(tmp, id+".log")
	logFile, err := os.Create(logPath)
	if err != nil {
		return "", ArgsError(fmt.Sprintf("shell: cannot write the job log: %s", syserr.Str(err)))
	}
	event := JobEvent{ID: id, SessionID: owner, SessionDir: sessionDir, Workspace: dir, CallID: callID, StartedAt: time.Now().UTC()}
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
		return "", ArgsError(fmt.Sprintf("shell: cannot start the command: %s", syserr.Str(err)))
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
	s.inner.mu.Lock()
	s.inner.byID[id] = &job{
		id: id, log: logPath, status: JobRunning,
		notify: notify, childPID: childPID,
	}
	s.inner.mu.Unlock()
	timeout := time.AfterFunc(limit, func() { s.timeOutJob(id) })
	go func() {
		err := child.Wait()
		timeout.Stop()
		untrack()
		s.finishJob(id, event, exitCodeOf(err))
	}()
	out := ShellResponse{JobID: &id, Status: JobRunning, Log: &logPath}
	data, merr := jsonx.Marshal(out)
	if merr != nil {
		return "", ArgsError(fmt.Sprintf("shell: encode the result: %v", merr))
	}
	return string(data), nil
}

// timeOutJob kills a background job that ran past its limit. The
// waiter in runBackground still reports the exit.
func (s *ShellTool) timeOutJob(id string) {
	s.inner.mu.Lock()
	j, ok := s.inner.byID[id]
	if !ok || j.finished {
		s.inner.mu.Unlock()
		return
	}
	j.finished = true
	j.status = JobTimeout
	pid := j.childPID
	s.inner.mu.Unlock()
	if pid != 0 {
		killProcessGroup(pid)
	}
	s.inner.changed.notify()
}

// finishJob records a background job's exit (unless it already ended,
// e.g. by timeout or cancel) and tells the observer and waiters.
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
	}
	s.inner.mu.Unlock()
	// The process has exited, so the bookkeeping sees all its writes.
	if s.observer != nil {
		final.EndedAt = time.Now().UTC()
		s.observer.JobFinished(final)
	}
	s.inner.changed.notify()
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

func (s *ShellTool) status(jobID string) (string, error) {
	s.inner.mu.Lock()
	j, ok := s.inner.byID[strings.TrimSpace(jobID)]
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
	text, err := shellResponseJSON(snap.id, snap.status, snap.exitCode, nil, nil, snap.log)
	if err != nil {
		return "", err
	}
	if snap.finished && (snap.status == JobFailed || snap.status == JobTimeout) {
		return "", WithContent(text, fmt.Sprintf("shell: job %s %s", snap.id, snap.status))
	}
	return text, nil
}

func (s *ShellTool) cancel(jobID string) (string, error) {
	var childPID int
	s.inner.mu.Lock()
	j, ok := s.inner.byID[strings.TrimSpace(jobID)]
	if !ok {
		s.inner.mu.Unlock()
		return "", ArgsError(fmt.Sprintf("shell: unknown job %q", jobID))
	}
	if !j.finished {
		j.finished = true
		j.status = JobCancelled
		childPID = j.childPID
	}
	s.inner.mu.Unlock()
	if childPID != 0 {
		killProcessGroup(childPID)
	}
	s.inner.changed.notify()
	s.inner.mu.Lock()
	jj, ok := s.inner.byID[strings.TrimSpace(jobID)]
	var snap job
	if ok {
		snap = *jj
	}
	s.inner.mu.Unlock()
	if !ok {
		return "", ArgsError(fmt.Sprintf("shell: unknown job %q", jobID))
	}
	return shellResponseJSON(snap.id, snap.status, snap.exitCode, nil, nil, snap.log)
}

func (s *ShellTool) ArmNotify(owner, callID, jobID string) {
	s.inner.mu.Lock()
	defer s.inner.mu.Unlock()
	if j, ok := s.inner.byID[strings.TrimSpace(jobID)]; ok {
		j.notify = true
		j.notifyCallID = callID
		j.owner = owner
	}
}

func (s *ShellTool) PendingNotify(owner string) bool {
	s.inner.mu.Lock()
	defer s.inner.mu.Unlock()
	for _, j := range s.inner.byID {
		if j.notify && j.owner == owner && !j.finished && !j.reported {
			return true
		}
	}
	return false
}

type NotifyResult struct {
	CallID    string
	JobID     string
	Status    string
	ExitCode  *int
	Output    string
	Truncated bool
	IsError   bool
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
		s.inner.mu.Unlock()
		tail, truncated := tailFile(log)
		out = append(out, NotifyResult{
			CallID: callID, JobID: jobID, Status: status, ExitCode: exitCode,
			Output: tail, Truncated: truncated,
			IsError: status == JobFailed || status == JobTimeout,
		})
	}
	return out
}

func (s *ShellTool) WaitPendingNotify(owner string, cancel context.Context) bool {
	for {
		s.inner.mu.Lock()
		pending := false
		for _, j := range s.inner.byID {
			if j.notify && j.owner == owner && !j.finished && !j.reported {
				pending = true
				break
			}
		}
		s.inner.mu.Unlock()
		if !pending {
			return true
		}
		select {
		case <-s.inner.changed.waitChan():
		case <-cancel.Done():
			return false
		}
	}
}

func tailFile(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	truncated := false
	slice := data
	if len(slice) > MaxNotifyOutput {
		slice = slice[len(slice)-MaxNotifyOutput:]
		truncated = true
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
