package runtime

import (
	"bufio"
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
	"github.com/Ady-JK/paladin/core/internal/session"
	"github.com/Ady-JK/paladin/core/internal/snapshot"
	"github.com/Ady-JK/paladin/core/internal/tools"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

const (
	stepsFile = "steps.jsonl"
	// snapTimeout bounds one snapshot so a huge folder can't wedge a
	// tool call; undo is best-effort, the call itself always runs.
	snapTimeout = 60 * time.Second
)

// Step is one file-changing tool run: the workspace before and after,
// and the files that differ. Turn is the transcript length when its
// turn started, i.e. the index its user message lands at.
type Step struct {
	Turn   int       `json:"turn"`
	CallID string    `json:"callId"`
	Tool   string    `json:"tool"`
	Before string    `json:"before"`
	After  string    `json:"after"`
	Files  []string  `json:"files"`
	At     time.Time `json:"at"`
}

// stepRecorder snapshots the workspace around each mutating tool run
// and appends the step to the session's steps.jsonl.
type stepRecorder struct {
	repo    *snapshot.Repo
	path    string
	turn    int
	session string
	jobs    *jobBook
}

// Track runs one mutating call inside the folder's exclusive bracket,
// so a concurrent call from another session in the same folder cannot
// land between the snapshots and be attributed to this one. Waiting for
// the bracket ends with the turn: the call then does not run.
func (s *stepRecorder) Track(ctx context.Context, callID, name string, run func() tools.ToolResult) tools.ToolResult {
	release, err := s.repo.Exclusive(ctx)
	if err != nil {
		return tools.ToolResult{CallID: callID, Name: name, IsError: true,
			Content: `{"error":"interrupted while waiting for another session's change in this folder to finish; it did not run"}`}
	}
	defer release()
	snapCtx, cancel := context.WithTimeout(context.Background(), snapTimeout)
	defer cancel()
	before, err := s.repo.Snapshot(snapCtx)
	result := run()
	if err != nil {
		log.Printf("undo: snapshot before %s: %v", callID, err)
		return result
	}
	snapCtx2, cancel2 := context.WithTimeout(context.Background(), snapTimeout)
	defer cancel2()
	after, err := s.repo.Snapshot(snapCtx2)
	if err != nil {
		log.Printf("undo: snapshot after %s: %v", callID, err)
		return result
	}
	if after == before {
		return result
	}
	files, err := s.repo.Changed(snapCtx2, before, after)
	if err != nil || len(files) == 0 {
		return result
	}
	if s.jobs != nil {
		s.jobs.noteCall(s.repo, s.session, callID, files)
	}
	step := Step{Turn: s.turn, CallID: callID, Tool: name, Before: before, After: after, Files: files, At: time.Now().UTC()}
	if err := appendStep(s.path, step); err != nil {
		log.Printf("undo: record step %s: %v", callID, err)
	}
	return result
}

// stepsMu orders steps.jsonl appends (turns and finishing background
// jobs) against commitRevert's rewrite.
var stepsMu sync.Mutex

func appendStep(path string, step Step) error {
	line, err := jsonx.Marshal(step)
	if err != nil {
		return err
	}
	stepsMu.Lock()
	defer stepsMu.Unlock()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

func readSteps(dir string) ([]Step, error) {
	f, err := os.Open(filepath.Join(dir, stepsFile))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []Step
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for sc.Scan() {
		var st Step
		// A torn last line (crash mid-append) is skipped, not fatal.
		if json.Unmarshal(sc.Bytes(), &st) == nil && st.Before != "" {
			out = append(out, st)
		}
	}
	return out, sc.Err()
}

func writeSteps(dir string, steps []Step) error {
	var buf []byte
	for _, st := range steps {
		line, err := jsonx.Marshal(st)
		if err != nil {
			return err
		}
		buf = append(append(buf, line...), '\n')
	}
	return atomicfile.Write(filepath.Join(dir, stepsFile), buf)
}

// snapRepo returns the snapshot repo for a workspace, or nil when undo
// snapshots are unavailable there (no git, refused folder, too large).
func (r *AgentRuntime) snapRepo(workspace string) *snapshot.Repo {
	if r.snaps == nil || workspace == "" {
		return nil
	}
	repo, err := r.snaps.Repo(workspace)
	if err != nil {
		return nil
	}
	return repo
}

// stepRecorderFor wires undo snapshots into a turn, or returns nil.
func (r *AgentRuntime) stepRecorderFor(sess transcript.Session, workspace string) *stepRecorder {
	repo := r.snapRepo(workspace)
	if repo == nil {
		return nil
	}
	msgs, err := sess.History()
	if err != nil {
		return nil
	}
	r.jobs.setTurn(sess.Meta.SessionID, len(msgs))
	return &stepRecorder{repo: repo, path: filepath.Join(sess.Dir, stepsFile), turn: len(msgs), session: sess.Meta.SessionID, jobs: r.jobs}
}

// RevertPreview describes what reverting to a user message would do.
type RevertPreview struct {
	Index     int      `json:"index"`
	Turns     int      `json:"turns"`
	Files     []string `json:"files"`
	Conflicts []string `json:"conflicts"`
	Snapshots bool     `json:"snapshots"`
}

// RevertResult reports a revert or redo.
type RevertResult struct {
	Index    int      `json:"index"`
	Message  string   `json:"message"`
	Restored []string `json:"restored"`
	Skipped  []string `json:"skipped"`
	// Failed files were due a restore that errored; they are left as
	// they are now and are also listed in Skipped.
	Failed    []string `json:"failed,omitempty"`
	Snapshots bool     `json:"snapshots"`
}

// revertPlan is the per-file outcome of reverting a set of steps.
type revertPlan struct {
	repo     *snapshot.Repo
	cur      string
	origin   map[string]string // path -> entry before the first step
	from     map[string]string // path -> tree holding that entry
	restore  []string
	conflict []string
	// interrupted are job records an earlier sidecar left running,
	// reconciled against the current folder; saved on a real revert.
	interrupted []JobRecord
}

func (r *AgentRuntime) checkRevertTarget(sess transcript.Session, index int) ([]transcript.Message, error) {
	msgs, err := sess.History()
	if err != nil {
		return nil, err
	}
	if index < 0 || index >= len(msgs) {
		return nil, fmt.Errorf("no message at index %d", index)
	}
	m := msgs[index]
	if m.Role != transcript.RoleUser || m.Replay || session.IsNotifyMessage(m) {
		return nil, fmt.Errorf("message %d is not a user message", index)
	}
	return msgs, nil
}

// planRevert works out which files to restore: for each file touched
// from index on, the version before its first change, but only if it
// still matches what this session last wrote. Anything else was changed
// since, by the user or another session, and is left alone.
func (r *AgentRuntime) planRevert(ctx context.Context, sess transcript.Session, index int) (*revertPlan, int, error) {
	repo := r.snapRepo(sess.Meta.Cwd)
	steps, err := readSteps(sess.Dir)
	if err != nil {
		return nil, 0, err
	}
	var picked []Step
	turns := map[int]bool{}
	for _, st := range steps {
		if st.Turn >= index {
			picked = append(picked, st)
			turns[st.Turn] = true
		}
	}
	if repo == nil {
		return nil, len(turns), nil
	}
	plan := &revertPlan{repo: repo, origin: map[string]string{}, from: map[string]string{}}
	expected := map[string]string{}
	for _, st := range picked {
		before, err := repo.Entries(ctx, st.Before, st.Files)
		if err != nil {
			return nil, 0, err
		}
		after, err := repo.Entries(ctx, st.After, st.Files)
		if err != nil {
			return nil, 0, err
		}
		for _, p := range st.Files {
			if _, seen := plan.origin[p]; !seen {
				plan.origin[p], plan.from[p] = before[p], st.Before
			}
			expected[p] = after[p]
		}
	}
	paths := make([]string, 0, len(plan.origin))
	for p := range plan.origin {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	cur, err := repo.Snapshot(ctx)
	if err != nil {
		return nil, 0, err
	}
	plan.cur = cur
	now, err := repo.Entries(ctx, cur, paths)
	if err != nil {
		return nil, 0, err
	}
	uncertain := map[string]bool{}
	for _, job := range readJobRecords(sess.Dir) {
		if job.Turn >= index {
			for _, p := range job.Uncertain {
				uncertain[p] = true
			}
		}
	}
	for _, job := range r.jobs.orphans(sess.Dir) {
		reconcileInterrupted(ctx, repo, &job, cur)
		for _, p := range job.Uncertain {
			uncertain[p] = true
			if _, stepped := plan.origin[p]; !stepped && job.Turn >= index {
				plan.conflict = append(plan.conflict, p)
			}
		}
		plan.interrupted = append(plan.interrupted, job)
	}
	for _, p := range paths {
		switch {
		case uncertain[p] && now[p] != plan.origin[p]:
			// A background job and another call both changed it.
			plan.conflict = append(plan.conflict, p)
		case now[p] == plan.origin[p]:
			// Already back to the original; nothing to write.
		case repo.Untracked(plan.origin[p]):
			// The original was too large or binary to capture.
			plan.conflict = append(plan.conflict, p)
		case now[p] == expected[p]:
			plan.restore = append(plan.restore, p)
		default:
			plan.conflict = append(plan.conflict, p)
		}
	}
	sort.Strings(plan.conflict)
	return plan, len(turns), nil
}

// reconcileInterrupted closes a job whose sidecar exited before it did.
// Its end was never seen, so everything that changed since it started
// may be its doing and none of it can be attributed or undone.
func reconcileInterrupted(ctx context.Context, repo *snapshot.Repo, job *JobRecord, cur string) {
	job.Status, job.Finalized = JobInterrupted, true
	if job.Before == "" {
		job.Note = "Paladin exited while this job ran, and it had no starting snapshot; its changes are not undoable"
		return
	}
	changed, err := repo.Changed(ctx, job.Before, cur)
	if err != nil {
		job.Note = "Paladin exited while this job ran, and its changes could not be compared; they are not undoable"
		return
	}
	job.Uncertain = changed
	job.Note = "Paladin exited while this job ran, so every change since it started is left alone by undo"
}

func (r *AgentRuntime) RevertPreview(sessionID, cwd string, index int) (RevertPreview, error) {
	sess, err := r.transcripts.Load(cwd, sessionID)
	if err != nil {
		return RevertPreview{}, err
	}
	if _, err := r.checkRevertTarget(sess, index); err != nil {
		return RevertPreview{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), snapTimeout)
	defer cancel()
	plan, turns, err := r.planRevert(ctx, sess, index)
	if err != nil {
		return RevertPreview{}, err
	}
	out := RevertPreview{Index: index, Turns: turns, Files: []string{}, Conflicts: []string{}}
	if plan != nil {
		out.Snapshots = true
		out.Files = nonNil(plan.restore)
		out.Conflicts = nonNil(plan.conflict)
	}
	return out, nil
}

// RevertSession rolls the session back to just before the user message
// at index: files this session changed since go back to how they were,
// and the conversation from there on is hidden until the next message
// commits the revert (or a redo undoes it).
func (r *AgentRuntime) RevertSession(sessionID, cwd string, index int) (RevertResult, error) {
	r.undoMu.Lock()
	defer r.undoMu.Unlock()
	if r.hub.Busy(sessionID) {
		return RevertResult{}, fmt.Errorf("a turn is running for this session; stop it first")
	}
	sess, err := r.transcripts.Load(cwd, sessionID)
	if err != nil {
		return RevertResult{}, err
	}
	if err := r.jobs.checkIdle(sessionID, sess.Dir); err != nil {
		return RevertResult{}, err
	}
	if sess.Meta.Revert != nil {
		if _, err := r.unrevert(sessionID, cwd); err != nil {
			return RevertResult{}, fmt.Errorf("redo the pending revert first: %w", err)
		}
		if sess, err = r.transcripts.Load(cwd, sessionID); err != nil {
			return RevertResult{}, err
		}
	}
	msgs, err := r.checkRevertTarget(sess, index)
	if err != nil {
		return RevertResult{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), snapTimeout)
	defer cancel()
	release, err := r.exclusive(ctx, sess.Meta.Cwd)
	if err != nil {
		return RevertResult{}, err
	}
	defer release()
	plan, _, err := r.planRevert(ctx, sess, index)
	if err != nil {
		return RevertResult{}, err
	}
	rv := &transcript.Revert{Index: index}
	var failed []string
	if plan != nil {
		for i := range plan.interrupted {
			if err := writeJobRecord(&plan.interrupted[i]); err != nil {
				log.Printf("undo: close interrupted job %s: %v", plan.interrupted[i].ID, err)
			}
		}
		rv.Snapshots, rv.Pre, rv.Skipped = true, plan.cur, plan.conflict
		byTree := map[string][]string{}
		for _, p := range plan.restore {
			byTree[plan.from[p]] = append(byTree[plan.from[p]], p)
		}
		var restoreErr error
		for tree, paths := range byTree {
			if err := plan.repo.Restore(ctx, tree, paths); err != nil {
				restoreErr = err
			}
		}
		restored := plan.restore
		if restoreErr != nil {
			log.Printf("undo: revert restore: %v", restoreErr)
			if restored, failed, err = settled(ctx, plan.repo, plan.restore, plan.origin); err != nil {
				return RevertResult{}, fmt.Errorf("restore files: %w", restoreErr)
			}
			rv.Skipped = append(rv.Skipped, failed...)
		}
		for _, p := range restored {
			rv.Restored = append(rv.Restored, transcript.RevertFile{Path: p, Wrote: plan.origin[p]})
		}
	}
	sess.Meta.Revert = rv
	if err := sess.SaveMeta(); err != nil {
		return RevertResult{}, err
	}
	text := msgs[index].Display
	if text == "" {
		text = msgs[index].Content
	}
	view := session.RevertView(rv)
	return RevertResult{Index: index, Message: text, Restored: view.Restored, Skipped: view.Skipped, Failed: failed, Snapshots: rv.Snapshots}, nil
}

// settled splits paths after a failed restore into those that now hold
// their wanted entry and those that do not.
func settled(ctx context.Context, repo *snapshot.Repo, paths []string, want map[string]string) (done, failed []string, err error) {
	cur, err := repo.Snapshot(ctx)
	if err != nil {
		return nil, nil, err
	}
	now, err := repo.Entries(ctx, cur, paths)
	if err != nil {
		return nil, nil, err
	}
	for _, p := range paths {
		if now[p] == want[p] {
			done = append(done, p)
		} else {
			failed = append(failed, p)
		}
	}
	return done, failed, nil
}

// exclusive takes the folder's mutation bracket (see Repo.Exclusive);
// a no-op where snapshots are off.
func (r *AgentRuntime) exclusive(ctx context.Context, workspace string) (func(), error) {
	repo := r.snapRepo(workspace)
	if repo == nil {
		return func() {}, nil
	}
	return repo.Exclusive(ctx)
}

// UnrevertSession redoes a pending revert: files go back to how they
// were just before it, unless they changed again since.
func (r *AgentRuntime) UnrevertSession(sessionID, cwd string) (RevertResult, error) {
	r.undoMu.Lock()
	defer r.undoMu.Unlock()
	return r.unrevert(sessionID, cwd)
}

func (r *AgentRuntime) unrevert(sessionID, cwd string) (RevertResult, error) {
	if r.hub.Busy(sessionID) {
		return RevertResult{}, fmt.Errorf("a turn is running for this session; stop it first")
	}
	sess, err := r.transcripts.Load(cwd, sessionID)
	if err != nil {
		return RevertResult{}, err
	}
	if err := r.jobs.checkIdle(sessionID, sess.Dir); err != nil {
		return RevertResult{}, err
	}
	rv := sess.Meta.Revert
	if rv == nil {
		return RevertResult{}, fmt.Errorf("nothing to redo")
	}
	out := RevertResult{Index: rv.Index, Restored: []string{}, Skipped: []string{}, Snapshots: rv.Snapshots}
	if repo := r.snapRepo(sess.Meta.Cwd); repo != nil && rv.Pre != "" && len(rv.Restored) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), snapTimeout)
		defer cancel()
		release, err := repo.Exclusive(ctx)
		if err != nil {
			return RevertResult{}, err
		}
		defer release()
		paths := make([]string, len(rv.Restored))
		for i, f := range rv.Restored {
			paths[i] = f.Path
		}
		cur, err := repo.Snapshot(ctx)
		if err != nil {
			return RevertResult{}, err
		}
		now, err := repo.Entries(ctx, cur, paths)
		if err != nil {
			return RevertResult{}, err
		}
		var back []string
		for _, f := range rv.Restored {
			if now[f.Path] == f.Wrote {
				back = append(back, f.Path)
			} else {
				out.Skipped = append(out.Skipped, f.Path)
			}
		}
		if len(back) > 0 {
			if restoreErr := repo.Restore(ctx, rv.Pre, back); restoreErr != nil {
				log.Printf("undo: redo restore: %v", restoreErr)
				pre, err := repo.Entries(ctx, rv.Pre, back)
				if err != nil {
					return RevertResult{}, fmt.Errorf("restore files: %w", restoreErr)
				}
				if back, out.Failed, err = settled(ctx, repo, back, pre); err != nil {
					return RevertResult{}, fmt.Errorf("restore files: %w", restoreErr)
				}
			}
		}
		out.Skipped = append(out.Skipped, out.Failed...)
		out.Restored = nonNil(back)
	}
	sess.Meta.Revert = nil
	if err := sess.SaveMeta(); err != nil {
		return RevertResult{}, err
	}
	return out, nil
}

// commitRevert makes a pending revert permanent: the hidden messages and
// their steps are dropped. Runs at the start of the session's next turn.
func (r *AgentRuntime) commitRevert(sess *transcript.Session) error {
	rv := sess.Meta.Revert
	if rv == nil {
		return nil
	}
	if err := sess.Truncate(rv.Index); err != nil {
		return err
	}
	stepsMu.Lock()
	defer stepsMu.Unlock()
	steps, err := readSteps(sess.Dir)
	if err == nil && len(steps) > 0 {
		kept := steps[:0]
		for _, st := range steps {
			if st.Turn < rv.Index {
				kept = append(kept, st)
			}
		}
		if err := writeSteps(sess.Dir, kept); err != nil {
			log.Printf("undo: prune steps: %v", err)
		}
	}
	sess.Meta.Revert = nil
	return sess.SaveMeta()
}

// visibleMessages hides a pending revert's messages.
func visibleMessages(meta transcript.Meta, msgs []transcript.Message) []transcript.Message {
	if meta.Revert != nil && meta.Revert.Index >= 0 && meta.Revert.Index < len(msgs) {
		return msgs[:meta.Revert.Index]
	}
	return msgs
}

// snapshotStore keeps undo snapshots under the data dir. PALADIN_SNAPSHOTS=off
// disables them (conversation-only undo).
func snapshotStore(dataDir string) *snapshot.Store {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("PALADIN_SNAPSHOTS")), "off") || !snapshot.Supported() {
		return nil
	}
	return snapshot.NewStore(filepath.Join(dataDir, "snapshots"))
}
