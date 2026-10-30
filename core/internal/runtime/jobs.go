// jobs.go is undo bookkeeping for background shell jobs. A job outlives
// the call that started it, so its writes land outside that call's
// snapshot bracket. Each job gets a record in <session>/jobs/<id>.json
// holding a snapshot from just before it spawned; when it ends (exit,
// cancel or timeout) the record is finalized once with a snapshot taken
// after the process exited. Files that other calls in the same folder
// also changed while the job ran cannot be attributed, so they are
// marked uncertain and never restored automatically.

package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Ady-JK/paladin/core/internal/atomicfile"
	"github.com/Ady-JK/paladin/core/internal/jsonx"
	"github.com/Ady-JK/paladin/core/internal/snapshot"
	"github.com/Ady-JK/paladin/core/internal/tools"
)

const jobsDir = "jobs"

// JobInterrupted marks a job whose sidecar exited before the job did:
// its end was never observed, so nothing it changed is attributable.
const JobInterrupted = "interrupted"

// JobRecord is a background job's persisted undo bookkeeping.
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
	// PID is the job's process group, used to tell whether a job left
	// running by an earlier sidecar process is still alive.
	PID    int    `json:"pid,omitempty"`
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
	// Files changed while the job ran and are attributed to it.
	Files []string `json:"files,omitempty"`
	// Uncertain files changed while the job ran but other calls touched
	// them too; a revert leaves them alone.
	Uncertain []string `json:"uncertain,omitempty"`
	Note      string   `json:"note,omitempty"`
	Finalized bool     `json:"finalized"`

	dir      string
	repo     *snapshot.Repo
	startSeq uint64
	overlap  bool
}

// activity is one bracketed call's changed files, kept while a job in
// the same folder is running so the job can tell which files others
// touched.
type activity struct {
	seq     uint64
	session string
	callID  string
	files   []string
}

type jobBook struct {
	rt *AgentRuntime

	mu       sync.Mutex
	live     map[string]*JobRecord
	turns    map[string]int
	seq      uint64
	activity map[*snapshot.Repo][]activity
}

func newJobBook(rt *AgentRuntime) *jobBook {
	return &jobBook{rt: rt, live: map[string]*JobRecord{}, turns: map[string]int{}, activity: map[*snapshot.Repo][]activity{}}
}

// setTurn records the transcript index of a session's running turn.
func (b *jobBook) setTurn(sessionID string, turn int) {
	b.mu.Lock()
	b.turns[sessionID] = turn
	b.mu.Unlock()
}

// noteCall records a bracketed call's changed files when a job in the
// folder could be affected.
func (b *jobBook) noteCall(repo *snapshot.Repo, session, callID string, files []string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.runningIn(repo) {
		return
	}
	b.seq++
	b.activity[repo] = append(b.activity[repo], activity{seq: b.seq, session: session, callID: callID, files: files})
}

func (b *jobBook) runningIn(repo *snapshot.Repo) bool {
	for _, rec := range b.live {
		if rec.repo == repo {
			return true
		}
	}
	return false
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
		StartedAt: e.StartedAt, Status: tools.JobRunning, dir: e.SessionDir, Turn: -1}
	rec.repo = b.rt.snapRepo(e.Workspace)
	if rec.repo != nil {
		ctx, cancel := context.WithTimeout(context.Background(), snapTimeout)
		before, err := rec.repo.Snapshot(ctx)
		cancel()
		if err != nil {
			rec.Note = "no snapshot before the job started; its changes are not undoable"
		}
		rec.Before = before
	}
	b.mu.Lock()
	if t, ok := b.turns[e.SessionID]; ok {
		rec.Turn = t
	}
	rec.startSeq = b.seq
	for _, other := range b.live {
		if rec.repo != nil && other.repo == rec.repo {
			other.overlap, rec.overlap = true, true
		}
	}
	b.live[e.ID] = rec
	b.mu.Unlock()
	if err := writeJobRecord(rec); err != nil {
		log.Printf("undo: record job %s: %v", e.ID, err)
	}
}

func (b *jobBook) JobSpawned(e tools.JobEvent) {
	b.mu.Lock()
	rec := b.live[e.ID]
	if rec != nil {
		rec.PID = e.PID
	}
	b.mu.Unlock()
	if rec != nil {
		if err := writeJobRecord(rec); err != nil {
			log.Printf("undo: record job %s pid: %v", e.ID, err)
		}
	}
}

func (b *jobBook) JobFinished(e tools.JobEvent) {
	b.mu.Lock()
	rec := b.live[e.ID]
	b.mu.Unlock()
	if rec == nil || rec.Finalized {
		return
	}
	rec.Status, rec.ExitCode, rec.EndedAt = e.Status, e.ExitCode, e.EndedAt
	if rec.repo != nil && rec.Before != "" && rec.Turn >= 0 {
		b.attribute(rec)
	}
	b.mu.Lock()
	rec.Finalized = true
	delete(b.live, e.ID)
	b.pruneActivity(rec.repo)
	b.mu.Unlock()
	if err := writeJobRecord(rec); err != nil {
		log.Printf("undo: finalize job %s: %v", e.ID, err)
	}
}

// attribute snapshots the folder after the job and splits what changed
// into files the job alone touched (recorded as an undo step) and files
// other calls touched too (uncertain).
func (b *jobBook) attribute(rec *JobRecord) {
	ctx, cancel := context.WithTimeout(context.Background(), snapTimeout)
	defer cancel()
	release, err := rec.repo.Exclusive(ctx)
	if err != nil {
		rec.Note = "the folder stayed busy, so the job's changes were not recorded"
		return
	}
	defer release()
	after, err := rec.repo.Snapshot(ctx)
	if err != nil {
		rec.Note = "no snapshot after the job ended; its changes are not undoable"
		return
	}
	rec.After = after
	if after == rec.Before {
		return
	}
	changed, err := rec.repo.Changed(ctx, rec.Before, after)
	if err != nil {
		rec.Note = "could not compare snapshots; the job's changes are not undoable"
		return
	}
	b.mu.Lock()
	others := map[string]bool{}
	for _, a := range b.activity[rec.repo] {
		if a.seq <= rec.startSeq || (a.callID == rec.CallID && a.session == rec.SessionID) {
			continue
		}
		for _, f := range a.files {
			others[f] = true
		}
	}
	overlap := rec.overlap
	b.mu.Unlock()
	for _, f := range changed {
		if overlap || others[f] {
			rec.Uncertain = append(rec.Uncertain, f)
		} else {
			rec.Files = append(rec.Files, f)
		}
	}
	if overlap {
		rec.Note = "another background job ran in this folder at the same time, so no change can be attributed safely"
	}
	if len(rec.Files) == 0 {
		return
	}
	step := Step{Turn: rec.Turn, CallID: rec.CallID, Tool: "shell", Before: rec.Before, After: after, Files: rec.Files, At: time.Now().UTC()}
	if err := appendStep(filepath.Join(rec.dir, stepsFile), step); err != nil {
		log.Printf("undo: record job step %s: %v", rec.ID, err)
	}
}

// pruneActivity drops activity no running job in the folder needs.
func (b *jobBook) pruneActivity(repo *snapshot.Repo) {
	if repo == nil {
		return
	}
	oldest, any := ^uint64(0), false
	for _, rec := range b.live {
		if rec.repo == repo {
			any = true
			if rec.startSeq < oldest {
				oldest = rec.startSeq
			}
		}
	}
	if !any {
		delete(b.activity, repo)
		return
	}
	kept := b.activity[repo][:0]
	for _, a := range b.activity[repo] {
		if a.seq > oldest {
			kept = append(kept, a)
		}
	}
	b.activity[repo] = kept
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
		return fmt.Errorf("background job %s from an earlier run of Paladin is still running, so its changes cannot be attributed; stop that process group or wait for it to exit, then retry", strings.Join(alive, ", "))
	}
	return nil
}

// errJobsRunning refuses a revert while the session's jobs could still
// be writing.
func errJobsRunning(ids []string) error {
	return fmt.Errorf("background job %s is still running, so its changes cannot be attributed safely yet; cancel it or wait for it to finish, then retry", strings.Join(ids, ", "))
}
