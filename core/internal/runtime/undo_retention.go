// undo_retention.go records, retains, and collects per-turn snapshots.

package runtime

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Ady-JK/paladin/core/internal/atomicfile"
	"github.com/Ady-JK/paladin/core/internal/jsonx"
	"github.com/Ady-JK/paladin/core/internal/redact"
	"github.com/Ady-JK/paladin/core/internal/session"
	"github.com/Ady-JK/paladin/core/internal/snapshot"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

const (
	turnsFile     = "turns.jsonl"
	undoFloorFile = "undo-floor.json"
	maxUndoTurns  = 3
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
func (r *AgentRuntime) recordTurnSnapshot(sess transcript.Session, workspace string) (status session.SnapshotProtection) {
	r.undoMu.Lock()
	defer r.undoMu.Unlock()
	status = session.SnapshotProtection{Turn: -1, State: "unavailable", Reason: "Snapshot could not be created."}
	history, err := readUndoHistory(sess)
	if err != nil {
		status.Reason = "Could not read the conversation to identify this turn."
		return
	}
	status.Turn = history.length
	defer func() {
		if err := saveProtection(sess.Dir, status); err != nil {
			log.Printf("undo: save protection status: %v", err)
			status.State = "unknown"
			status.Reason = "Could not save snapshot protection details."
		}
	}()
	if err := advanceUndoFloor(sess.Dir, history.starts, true); err != nil {
		log.Printf("undo: update retention boundary: %v", err)
		status.Reason = "Could not update the undo history limit."
		return
	}
	status.Floor, err = undoFloor(sess.Dir)
	if err != nil {
		status.Reason = "Could not read the undo history limit."
		return
	}
	if r.snaps == nil {
		if strings.EqualFold(strings.TrimSpace(os.Getenv("PALADIN_SNAPSHOTS")), "off") {
			status.Reason = "Snapshots are disabled."
		} else {
			status.Reason = "Snapshots are unavailable because Git could not be found."
		}
		return
	}
	repo, repoErr := r.snaps.Repo(workspace)
	if repoErr != nil {
		status.Reason = "Snapshot unavailable: " + repoErr.Error()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), snapTimeout)
	defer cancel()
	tree, err := repo.Snapshot(ctx)
	if err != nil {
		log.Printf("undo: snapshot turn %d: %v", history.length, err)
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			status.Reason = "Snapshot timed out."
		case errors.Is(err, snapshot.ErrTooLarge):
			status.Reason = "Folder contains too many files to snapshot."
		default:
			status.Reason = "Snapshot failed: " + redact.String(err.Error())
		}
	}
	all, readErr := readAllTurnSnapshots(sess.Dir)
	if readErr != nil {
		log.Printf("undo: read snapshots: %v", readErr)
		status.Reason = "Could not read saved snapshot history."
		return
	}
	floor, readErr := undoFloor(sess.Dir)
	if readErr != nil {
		log.Printf("undo: read retention boundary: %v", readErr)
		status.Reason = "Could not read the undo history limit."
		return
	}
	list := make([]TurnSnapshot, 0, maxUndoTurns)
	for _, item := range all {
		if item.Turn >= floor && item.Turn != history.length {
			list = append(list, item)
		}
	}
	pruned := len(list) < len(all)
	if err == nil {
		list = append(list, TurnSnapshot{Turn: history.length, Tree: tree, At: time.Now().UTC()})
	}
	if len(list) > maxUndoTurns {
		list = list[len(list)-maxUndoTurns:]
		pruned = true
	}
	if err != nil && !pruned {
		return
	}
	if err := writeTurnSnapshots(sess.Dir, list); err != nil {
		log.Printf("undo: record turn %d: %v", history.length, err)
		status.Reason = "Could not save the snapshot reference."
		return
	}
	if err == nil {
		status.State, status.Reason = "protected", ""
		paths, truncated, listErr := repo.Excluded(ctx, tree, 100)
		if listErr != nil {
			status.State, status.Reason = "partial", "Snapshot saved, but excluded paths could not be listed."
			status.Truncated = true
		} else if len(paths) > 0 || truncated {
			status.State, status.Reason, status.Excluded, status.Truncated = "partial", "Some paths are excluded from snapshots.", paths, truncated
		}
	}
	if pruned {
		if err := r.collectSnapshots(ctx, repo); err != nil {
			log.Printf("undo: snapshot cleanup: %v", err)
		}
	}
	return
}

func undoFloor(dir string) (int, error) {
	data, err := os.ReadFile(filepath.Join(dir, undoFloorFile))
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var floor struct {
		Index int `json:"index"`
	}
	if err := json.Unmarshal(data, &floor); err != nil {
		return 0, err
	}
	if floor.Index < 0 {
		return 0, fmt.Errorf("invalid undo retention boundary")
	}
	return floor.Index, nil
}

// undoHistory retains grouped turns so undo can check targets, handle
// steering, and find the retention boundary after a single disk scan.
type undoHistory struct {
	turns  []transcript.Turn
	starts []int
	length int
}

func readUndoHistory(sess transcript.Session) (undoHistory, error) {
	turns, err := sess.HistoryTurns()
	if err != nil {
		return undoHistory{}, err
	}
	history := undoHistory{turns: turns}
	for _, turn := range turns {
		if len(turn.Messages) > 0 {
			msg := turn.Messages[0]
			if msg.Role == transcript.RoleUser && !msg.Replay && !session.IsNotifyMessage(msg) {
				history.starts = append(history.starts, history.length)
			}
		}
		history.length += len(turn.Messages)
	}
	return history, nil
}

// A steer is recorded inside its original turn. Reverting it must restore
// that turn's snapshot rather than silently rewinding only the transcript.
func (h undoHistory) revertTargetIndex(index int) int {
	start := 0
	for _, turn := range h.turns {
		end := start + len(turn.Messages)
		if index >= start && index < end && turn.Messages[index-start].Role == transcript.RoleUser &&
			len(turn.Messages) > 0 && turn.Messages[0].Role == transcript.RoleUser && !turn.Messages[0].Replay && !session.IsNotifyMessage(turn.Messages[0]) {
			return start
		}
		start = end
	}
	return index
}

// advanceUndoFloor makes the three-turn boundary permanent, even after
// undo truncates the conversation or snapshots are disabled.
func advanceUndoFloor(dir string, indices []int, addingTurn bool) error {
	keep := maxUndoTurns
	if addingTurn {
		keep--
	}
	if len(indices) <= keep {
		return nil
	}
	newFloor := indices[len(indices)-keep]
	oldFloor, err := undoFloor(dir)
	if err != nil {
		return err
	}
	if newFloor <= oldFloor {
		return nil
	}
	data, err := jsonx.Marshal(struct {
		Index int `json:"index"`
	}{Index: newFloor})
	if err != nil {
		return err
	}
	return atomicfile.Write(filepath.Join(dir, undoFloorFile), data)
}

func readAllTurnSnapshots(dir string) ([]TurnSnapshot, error) {
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

func readTurnSnapshots(dir string) ([]TurnSnapshot, error) {
	list, err := readAllTurnSnapshots(dir)
	if err != nil {
		return nil, err
	}
	floor, err := undoFloor(dir)
	if err != nil {
		return nil, err
	}
	kept := list[:0]
	for _, item := range list {
		if item.Turn >= floor {
			kept = append(kept, item)
		}
	}
	list = kept
	if len(list) > maxUndoTurns {
		list = list[len(list)-maxUndoTurns:]
	}
	return list, err
}

// collectSnapshots protects retained snapshots from every session before
// collecting objects in the shared workspace repository. The caller holds
// undoMu, so no local snapshot reader can lose its temporary tree.
func (r *AgentRuntime) collectSnapshots(ctx context.Context, repo *snapshot.Repo) error {
	var roots []string
	err := filepath.WalkDir(r.transcripts.Root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			rel, err := filepath.Rel(r.transcripts.Root, path)
			if err != nil {
				return err
			}
			if rel != "." && strings.Count(rel, string(filepath.Separator)) >= 2 {
				return filepath.SkipDir
			}
			return nil
		}
		switch entry.Name() {
		case recoveryFile:
			saved, err := readRecovery(filepath.Dir(path))
			if err != nil {
				return err
			}
			if saved.Tree != "" {
				roots = append(roots, saved.Tree)
			}
			for _, turn := range saved.Turns {
				roots = append(roots, turn.Tree)
			}
		case turnsFile:
			list, err := readTurnSnapshots(filepath.Dir(path))
			if err != nil {
				return err
			}
			for _, item := range list {
				roots = append(roots, item.Tree)
			}
		case "meta.json":
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			var meta transcript.Meta
			if err := json.Unmarshal(data, &meta); err != nil {
				return err
			}
			if meta.Revert != nil && meta.Revert.Pre != "" {
				roots = append(roots, meta.Revert.Pre)
			}
		}
		return nil
	})
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return repo.Collect(ctx, roots)
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
