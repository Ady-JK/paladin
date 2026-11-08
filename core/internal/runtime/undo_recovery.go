package runtime

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Ady-JK/paladin/core/internal/atomicfile"
	"github.com/Ady-JK/paladin/core/internal/session"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

const recoveryFile = "latest-snapshot.json"

// previewVersion covers both the folder and the conversation, so a new
// message cannot be silently discarded by an older confirmation.
func previewVersion(dir, tree string) (string, error) {
	f, err := os.Open(filepath.Join(dir, "messages.jsonl"))
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.WriteString(h, tree); err != nil {
		return "", err
	}
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// recoverySnapshot is one session's state immediately before its latest undo.
// The tree and retained turn trees are roots for snapshot garbage collection.
type recoverySnapshot struct {
	Tree       string                       `json:"tree,omitempty"`
	Transcript []byte                       `json:"transcript"`
	Turns      []TurnSnapshot               `json:"turns"`
	Floor      int                          `json:"floor"`
	Protection []session.SnapshotProtection `json:"protection,omitempty"`
}

func saveRecovery(sess transcript.Session, tree string) error {
	f, err := os.Open(filepath.Join(sess.Dir, "messages.jsonl"))
	if err != nil {
		return err
	}
	defer f.Close()
	var compressed bytes.Buffer
	zip, err := gzip.NewWriterLevel(&compressed, gzip.BestSpeed)
	if err != nil {
		return err
	}
	if _, err := io.Copy(zip, f); err != nil {
		return err
	}
	if err := zip.Close(); err != nil {
		return err
	}
	turns, err := readTurnSnapshots(sess.Dir)
	if err != nil {
		return err
	}
	floor, err := undoFloor(sess.Dir)
	if err != nil {
		return err
	}
	protection, err := readProtection(sess.Dir)
	if err != nil {
		return err
	}
	data, err := json.Marshal(recoverySnapshot{Tree: tree, Transcript: compressed.Bytes(), Turns: turns, Floor: floor, Protection: protection})
	if err != nil {
		return err
	}
	return atomicfile.Write(filepath.Join(sess.Dir, recoveryFile), data)
}

func readRecovery(dir string) (recoverySnapshot, error) {
	var s recoverySnapshot
	data, err := os.ReadFile(filepath.Join(dir, recoveryFile))
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return s, err
	}
	if s.Transcript == nil || s.Floor < 0 {
		return s, fmt.Errorf("invalid recovery snapshot")
	}
	return s, nil
}

func (r *AgentRuntime) RecoveryAvailable(sessionID, cwd string) (bool, error) {
	r.undoMu.Lock()
	defer r.undoMu.Unlock()
	sess, err := r.transcripts.Load(cwd, sessionID)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(filepath.Join(sess.Dir, recoveryFile))
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}

// RecoveryPreview lists the workspace changes a recovery would make.
func (r *AgentRuntime) RecoveryPreview(sessionID, cwd string) (RevertPreview, error) {
	r.undoMu.Lock()
	defer r.undoMu.Unlock()
	sess, err := r.transcripts.Load(cwd, sessionID)
	if err != nil {
		return RevertPreview{}, err
	}
	saved, err := readRecovery(sess.Dir)
	if err != nil {
		return RevertPreview{}, err
	}
	out := RevertPreview{Files: []string{}, Deleted: []string{}, Skipped: []string{}}
	if saved.Tree == "" {
		out.Version, err = previewVersion(sess.Dir, "")
		return out, err
	}
	repo := r.snapRepo(sess.Meta.Cwd)
	if repo == nil {
		return out, fmt.Errorf("recovery snapshot repository unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), snapTimeout)
	defer cancel()
	current, err := repo.Snapshot(ctx)
	if err != nil {
		return out, err
	}
	paths, err := repo.Changed(ctx, saved.Tree, current)
	if err != nil {
		return out, err
	}
	entries, err := repo.Entries(ctx, saved.Tree, paths)
	if err != nil {
		return out, err
	}
	currentEntries, err := repo.Entries(ctx, current, paths)
	if err != nil {
		return out, err
	}
	for _, p := range paths {
		if repo.Untracked(entries[p]) || repo.Untracked(currentEntries[p]) {
			out.Skipped = append(out.Skipped, p)
		} else if entries[p] == "" {
			out.Deleted = append(out.Deleted, p)
		} else {
			out.Files = append(out.Files, p)
		}
	}
	out.Snapshots = true
	out.Version, err = previewVersion(sess.Dir, current)
	if err != nil {
		return out, err
	}
	return out, nil
}

// RecoverLatest restores the state saved before the most recent undo. The
// snapshot remains available until a later undo replaces it.
func (r *AgentRuntime) RecoverLatest(sessionID, cwd string) (RevertResult, error) {
	return r.RecoverLatestChecked(sessionID, cwd, "")
}

func (r *AgentRuntime) RecoverLatestChecked(sessionID, cwd, version string) (RevertResult, error) {
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
	saved, err := readRecovery(sess.Dir)
	if err != nil {
		return RevertResult{}, fmt.Errorf("read recovery snapshot: %w", err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(saved.Transcript))
	if err != nil {
		return RevertResult{}, fmt.Errorf("read saved conversation: %w", err)
	}
	conversation, err := io.ReadAll(reader)
	closeErr := reader.Close()
	if err != nil {
		return RevertResult{}, fmt.Errorf("read saved conversation: %w", err)
	}
	if closeErr != nil {
		return RevertResult{}, fmt.Errorf("read saved conversation: %w", closeErr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), snapTimeout)
	defer cancel()
	release, err := r.exclusive(ctx, sess.Meta.Cwd)
	if err != nil {
		return RevertResult{}, err
	}
	defer release()
	out := RevertResult{Restored: []string{}, Deleted: []string{}, Skipped: []string{}}
	if saved.Tree == "" && version != "" {
		actual, err := previewVersion(sess.Dir, "")
		if err != nil {
			return out, err
		}
		if version != actual {
			return out, fmt.Errorf("conversation changed since the recovery preview; review it again")
		}
	}
	if saved.Tree != "" {
		repo := r.snapRepo(sess.Meta.Cwd)
		if repo == nil {
			return out, fmt.Errorf("recovery snapshot repository unavailable")
		}
		current, err := repo.Snapshot(ctx)
		if err != nil {
			return out, err
		}
		if version != "" {
			actual, err := previewVersion(sess.Dir, current)
			if err != nil {
				return out, err
			}
			if version != actual {
				return out, fmt.Errorf("folder or conversation changed since the recovery preview; review it again")
			}
		}
		paths, err := repo.Changed(ctx, saved.Tree, current)
		if err != nil {
			return out, err
		}
		entries, err := repo.Entries(ctx, saved.Tree, paths)
		if err != nil {
			return out, err
		}
		currentEntries, err := repo.Entries(ctx, current, paths)
		if err != nil {
			return out, err
		}
		var restore []string
		for _, p := range paths {
			if repo.Untracked(entries[p]) || repo.Untracked(currentEntries[p]) {
				out.Skipped = append(out.Skipped, p)
				continue
			}
			restore = append(restore, p)
			if entries[p] == "" {
				out.Deleted = append(out.Deleted, p)
			} else {
				out.Restored = append(out.Restored, p)
			}
		}
		if err := repo.Restore(ctx, saved.Tree, restore); err != nil {
			return out, fmt.Errorf("recover files: %w", err)
		}
		out.Snapshots = true
	}
	// Drop the list cache before replacing the transcript. A missing cache is
	// safe, including if any later write fails.
	if err := os.Remove(filepath.Join(sess.Dir, "summary.json")); err != nil && !os.IsNotExist(err) {
		return out, err
	}
	if err := atomicfile.Write(filepath.Join(sess.Dir, "messages.jsonl"), conversation); err != nil {
		return out, err
	}
	if err := writeTurnSnapshots(sess.Dir, saved.Turns); err != nil {
		return out, err
	}
	floor, err := json.Marshal(struct {
		Index int `json:"index"`
	}{saved.Floor})
	if err != nil {
		return out, err
	}
	if err := atomicfile.Write(filepath.Join(sess.Dir, undoFloorFile), floor); err != nil {
		return out, err
	}
	if err := writeProtection(sess.Dir, saved.Protection); err != nil {
		return out, err
	}
	return out, nil
}
