// undo.go records one workspace snapshot at the start of each user turn
// and restores it on revert. A separate per-session recovery snapshot
// preserves the state immediately before the most recent revert.

package runtime

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Ady-JK/paladin/core/internal/session"
	"github.com/Ady-JK/paladin/core/internal/snapshot"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

// RevertPreview describes what reverting to a user message would do.
type RevertPreview struct {
	Index      int                         `json:"index"`
	Turns      int                         `json:"turns"`
	Files      []string                    `json:"files"`
	Deleted    []string                    `json:"deleted"`
	Skipped    []string                    `json:"skipped"`
	Snapshots  bool                        `json:"snapshots"`
	Version    string                      `json:"version,omitempty"`
	Protection *session.SnapshotProtection `json:"protection,omitempty"`
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
	current string
	restore []string
	deleted []string
	skipped []string
}

func checkRevertTarget(sess transcript.Session, history undoHistory, index int) (transcript.Message, error) {
	if index < 0 || index >= history.length {
		return transcript.Message{}, fmt.Errorf("no message at index %d", index)
	}
	floor, err := undoFloor(sess.Dir)
	if err != nil {
		return transcript.Message{}, fmt.Errorf("read undo retention boundary: %w", err)
	}
	if index < floor {
		return transcript.Message{}, fmt.Errorf("only the last %d turns can be reverted", maxUndoTurns)
	}
	var m transcript.Message
	start := 0
	for _, turn := range history.turns {
		if index < start+len(turn.Messages) {
			m = turn.Messages[index-start]
			break
		}
		start += len(turn.Messages)
	}
	if m.Role != transcript.RoleUser || m.Replay || session.IsNotifyMessage(m) {
		return transcript.Message{}, fmt.Errorf("message %d is not a user message", index)
	}
	count, found := 0, false
	for _, start := range history.starts {
		if start == index {
			found = true
		}
		if start >= index {
			count++
		}
	}
	if !found {
		return transcript.Message{}, fmt.Errorf("message %d is not the start of a turn", index)
	}
	if count > maxUndoTurns {
		return transcript.Message{}, fmt.Errorf("only the last %d turns can be reverted", maxUndoTurns)
	}
	return m, nil
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
	currentEntries, err := repo.Entries(ctx, current, changed)
	if err != nil {
		return nil, count, err
	}
	plan := &revertPlan{repo: repo, target: target.Tree, current: current}
	for _, p := range changed {
		switch {
		case repo.Untracked(entries[p]) || repo.Untracked(currentEntries[p]):
			// Too large or binary to capture: leave the current file.
			plan.skipped = append(plan.skipped, p)
		case entries[p] == "":
			plan.deleted = append(plan.deleted, p)
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
	r.undoMu.Lock()
	defer r.undoMu.Unlock()
	sess, err := r.transcripts.Load(cwd, sessionID)
	if err != nil {
		return RevertPreview{}, err
	}
	history, err := readUndoHistory(sess)
	if err != nil {
		return RevertPreview{}, err
	}
	index = history.revertTargetIndex(index)
	if _, err := checkRevertTarget(sess, history, index); err != nil {
		return RevertPreview{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), snapTimeout)
	defer cancel()
	plan, count, err := r.planRevert(ctx, sess, index)
	if err != nil {
		return RevertPreview{}, err
	}
	out := RevertPreview{Index: index, Turns: count, Files: []string{}, Deleted: []string{}, Skipped: []string{}}
	entries, statusErr := readProtection(sess.Dir)
	if statusErr == nil {
		for _, p := range entries {
			if p.Turn == index {
				copy := p
				out.Protection = &copy
				break
			}
		}
	}
	current := ""
	if plan != nil {
		out.Snapshots = true
		current = plan.current
		out.Files = nonNil(plan.restore)
		out.Deleted = nonNil(plan.deleted)
		out.Skipped = nonNil(plan.skipped)
		if out.Protection == nil {
			out.Protection = &session.SnapshotProtection{Turn: index, State: "unknown", Reason: "Snapshot coverage details were not recorded for this turn."}
		}
		paths, truncated, err := plan.repo.Excluded(ctx, current, 100)
		if err != nil {
			out.Protection.Truncated = true
		} else {
			seen := make(map[string]bool, len(out.Protection.Excluded)+len(paths))
			var combined []string
			for _, p := range append(out.Protection.Excluded, paths...) {
				if seen[p] {
					continue
				}
				seen[p] = true
				if len(combined) == 100 {
					truncated = true
					continue
				}
				combined = append(combined, p)
			}
			sort.Strings(combined)
			out.Protection.Excluded = combined
			out.Protection.Truncated = out.Protection.Truncated || truncated
		}
	}
	out.Version, err = previewVersion(sess.Dir, current)
	if err != nil {
		return RevertPreview{}, err
	}
	return out, nil
}

// RevertSession saves a recovery snapshot, rolls the folder back to the
// snapshot taken before the message, then truncates the conversation.
func (r *AgentRuntime) RevertSession(sessionID, cwd string, index int) (RevertResult, error) {
	return r.RevertSessionChecked(sessionID, cwd, index, "")
}

// RevertSessionChecked rejects an out-of-date preview before changing files.
func (r *AgentRuntime) RevertSessionChecked(sessionID, cwd string, index int, version string) (RevertResult, error) {
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
	history, err := readUndoHistory(sess)
	if err != nil {
		return RevertResult{}, err
	}
	index = history.revertTargetIndex(index)
	msg, err := checkRevertTarget(sess, history, index)
	if err != nil {
		return RevertResult{}, err
	}
	text := msg.Display
	if text == "" {
		text = msg.Content
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
	current := ""
	if plan != nil {
		current = plan.current
	}
	if version != "" {
		actual, err := previewVersion(sess.Dir, current)
		if err != nil {
			return RevertResult{}, err
		}
		if version != actual {
			return RevertResult{}, fmt.Errorf("folder or conversation changed since the undo preview; review it again")
		}
	}
	if err := saveRecovery(sess, current); err != nil {
		return RevertResult{}, fmt.Errorf("save recovery snapshot: %w", err)
	}
	if err := advanceUndoFloor(sess.Dir, history.starts, false); err != nil {
		return RevertResult{}, fmt.Errorf("save undo retention boundary: %w", err)
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
	if err := pruneProtection(sess.Dir, index); err != nil {
		log.Printf("undo: prune protection status: %v", err)
	}
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
