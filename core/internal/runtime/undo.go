// undo.go records one workspace snapshot at the start of each user turn
// and restores it on revert. There is no per-call bracket and no folder
// lock around commands, and a revert is final: it puts the folder back
// exactly as it was before the message, so it cannot be undone.

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
	"time"

	"github.com/Ady-JK/paladin/core/internal/atomicfile"
	"github.com/Ady-JK/paladin/core/internal/jsonx"
	"github.com/Ady-JK/paladin/core/internal/session"
	"github.com/Ady-JK/paladin/core/internal/snapshot"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

const (
	turnsFile = "turns.jsonl"
	// snapTimeout bounds one snapshot so a huge folder can't wedge a
	// turn; undo is best-effort, the turn itself always runs.
	snapTimeout = 60 * time.Second
)

// TurnSnapshot is the folder state captured just before a user turn.
type TurnSnapshot struct {
	Turn int       `json:"turn"`
	Tree string    `json:"tree"`
	At   time.Time `json:"at"`
}

// recordTurnSnapshot captures the folder before a user message starts
// its work. One snapshot per turn is the whole undo model.
func (r *AgentRuntime) recordTurnSnapshot(sess transcript.Session, workspace string) {
	repo := r.snapRepo(workspace)
	if repo == nil {
		return
	}
	msgs, err := sess.History()
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), snapTimeout)
	defer cancel()
	tree, err := repo.Snapshot(ctx)
	if err != nil {
		log.Printf("undo: snapshot turn %d: %v", len(msgs), err)
		return
	}
	entry := TurnSnapshot{Turn: len(msgs), Tree: tree, At: time.Now().UTC()}
	if err := appendTurnSnapshot(sess.Dir, entry); err != nil {
		log.Printf("undo: record turn %d: %v", entry.Turn, err)
	}
}

func appendTurnSnapshot(dir string, e TurnSnapshot) error {
	line, err := jsonx.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, turnsFile), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

func readTurnSnapshots(dir string) ([]TurnSnapshot, error) {
	f, err := os.Open(filepath.Join(dir, turnsFile))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []TurnSnapshot
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		var e TurnSnapshot
		// A torn last line (crash mid-append) is skipped, not fatal.
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.Tree != "" {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Turn < out[j].Turn })
	return out, sc.Err()
}

func writeTurnSnapshots(dir string, list []TurnSnapshot) error {
	var buf []byte
	for _, e := range list {
		line, err := jsonx.Marshal(e)
		if err != nil {
			return err
		}
		buf = append(append(buf, line...), '\n')
	}
	return atomicfile.Write(filepath.Join(dir, turnsFile), buf)
}

// pruneTurnSnapshots drops the snapshots of turns a revert removed.
func pruneTurnSnapshots(dir string, keepBefore int) {
	list, err := readTurnSnapshots(dir)
	if err != nil || len(list) == 0 {
		return
	}
	kept := list[:0]
	for _, e := range list {
		if e.Turn < keepBefore {
			kept = append(kept, e)
		}
	}
	if len(kept) == len(list) {
		return
	}
	if err := writeTurnSnapshots(dir, kept); err != nil {
		log.Printf("undo: prune turn snapshots: %v", err)
	}
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

// RevertPreview describes what reverting to a user message would do.
type RevertPreview struct {
	Index     int      `json:"index"`
	Turns     int      `json:"turns"`
	Files     []string `json:"files"`
	Deleted   []string `json:"deleted"`
	Skipped   []string `json:"skipped"`
	Snapshots bool     `json:"snapshots"`
}

// RevertResult reports a revert.
type RevertResult struct {
	Index    int      `json:"index"`
	Message  string   `json:"message"`
	Restored []string `json:"restored"`
	Deleted  []string `json:"deleted"`
	Skipped  []string `json:"skipped"`
	// Failed files were due a restore that errored; they are left as
	// they are now and are also listed in Skipped.
	Failed    []string `json:"failed,omitempty"`
	Snapshots bool     `json:"snapshots"`
}

// revertPlan is what a revert would change: paths to restore from the
// turn snapshot and paths to delete because they did not exist then.
type revertPlan struct {
	repo    *snapshot.Repo
	target  string
	restore []string
	deleted []string
	skipped []string
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

// planRevert compares the turn's snapshot with the folder now. Every
// difference is restored; nothing is skipped because another session
// changed it (the user asked for the folder to match the snapshot).
func (r *AgentRuntime) planRevert(ctx context.Context, sess transcript.Session, index int) (*revertPlan, int, error) {
	repo := r.snapRepo(sess.Meta.Cwd)
	if repo == nil {
		return nil, 0, nil
	}
	turns, err := readTurnSnapshots(sess.Dir)
	if err != nil {
		return nil, 0, err
	}
	count := 0
	var target TurnSnapshot
	found := false
	for _, t := range turns {
		if t.Turn >= index {
			count++
		}
		if t.Turn == index {
			target, found = t, true
		}
	}
	if !found {
		return nil, count, nil
	}
	current, err := repo.Snapshot(ctx)
	if err != nil {
		return nil, count, err
	}
	changed, err := repo.Changed(ctx, target.Tree, current)
	if err != nil {
		return nil, count, err
	}
	entries, err := repo.Entries(ctx, target.Tree, changed)
	if err != nil {
		return nil, count, err
	}
	plan := &revertPlan{repo: repo, target: target.Tree}
	for _, p := range changed {
		switch {
		case entries[p] == "":
			plan.deleted = append(plan.deleted, p)
		case repo.Untracked(entries[p]):
			// Too large or binary to capture: leave the current file.
			plan.skipped = append(plan.skipped, p)
		default:
			plan.restore = append(plan.restore, p)
		}
	}
	sort.Strings(plan.restore)
	sort.Strings(plan.deleted)
	sort.Strings(plan.skipped)
	return plan, count, nil
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
	plan, count, err := r.planRevert(ctx, sess, index)
	if err != nil {
		return RevertPreview{}, err
	}
	out := RevertPreview{Index: index, Turns: count, Files: []string{}, Deleted: []string{}, Skipped: []string{}}
	if plan != nil {
		out.Snapshots = true
		out.Files = nonNil(plan.restore)
		out.Deleted = nonNil(plan.deleted)
		out.Skipped = nonNil(plan.skipped)
	}
	return out, nil
}

// RevertSession rolls the folder back to the snapshot taken before the
// user message at index, then drops that message and everything after
// it. The restore is final: there is no redo.
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
	msgs, err := r.checkRevertTarget(sess, index)
	if err != nil {
		return RevertResult{}, err
	}
	text := msgs[index].Display
	if text == "" {
		text = msgs[index].Content
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
	out := RevertResult{Index: index, Message: text, Restored: []string{}, Deleted: []string{}, Skipped: []string{}}
	if plan != nil {
		out.Snapshots = true
		out.Restored = nonNil(plan.restore)
		out.Deleted = nonNil(plan.deleted)
		out.Skipped = nonNil(plan.skipped)
		paths := append(append([]string{}, plan.restore...), plan.deleted...)
		if len(paths) > 0 {
			if err := plan.repo.Restore(ctx, plan.target, paths); err != nil {
				log.Printf("undo: revert restore: %v", err)
				done, failed, serr := settled(ctx, plan.repo, plan.target, paths)
				if serr != nil {
					return RevertResult{}, fmt.Errorf("restore files: %w", err)
				}
				out.Restored = nonNil(done)
				out.Failed = failed
				out.Skipped = append(out.Skipped, failed...)
			}
		}
	}
	if err := sess.Truncate(index); err != nil {
		return RevertResult{}, err
	}
	pruneTurnSnapshots(sess.Dir, index)
	sess.Meta.Revert = nil
	if err := sess.SaveMeta(); err != nil {
		return RevertResult{}, err
	}
	return out, nil
}

// settled splits paths after a failed restore into those that now match
// the target snapshot and those that do not.
func settled(ctx context.Context, repo *snapshot.Repo, target string, paths []string) (done, failed []string, err error) {
	cur, err := repo.Snapshot(ctx)
	if err != nil {
		return nil, nil, err
	}
	want, err := repo.Entries(ctx, target, paths)
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

// exclusive takes the folder's mutation bracket for a revert; commands
// no longer hold it, so only another revert waits.
func (r *AgentRuntime) exclusive(ctx context.Context, workspace string) (func(), error) {
	repo := r.snapRepo(workspace)
	if repo == nil {
		return func() {}, nil
	}
	return repo.Exclusive(ctx)
}

// commitRevert finishes a revert written by an older version that kept
// the transcript hidden until the next message. New reverts truncate
// immediately, so this is only an upgrade path.
func (r *AgentRuntime) commitRevert(sess *transcript.Session) error {
	rv := sess.Meta.Revert
	if rv == nil {
		return nil
	}
	if err := sess.Truncate(rv.Index); err != nil {
		return err
	}
	pruneTurnSnapshots(sess.Dir, rv.Index)
	sess.Meta.Revert = nil
	return sess.SaveMeta()
}

// visibleMessages hides a legacy pending revert's messages.
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
