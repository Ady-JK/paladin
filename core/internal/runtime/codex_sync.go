package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/Ady-JK/paladin/core/internal/atomicfile"
	"github.com/Ady-JK/paladin/core/internal/codex"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

// A linked session mirrors Codex's persisted turns. Reading them does not resume
// the thread, take its writer, or start a model call in either app.
type codexSyncState struct {
	mu       sync.Mutex
	threadID string
	seen     map[string][32]byte
	latest   [32]byte
	checked  time.Time
}

type codexSyncCheckpoint struct {
	ThreadID string              `json:"threadId"`
	Seen     map[string][32]byte `json:"turns"`
}

const codexSyncFile = "codex-sync.json"

func saveCodexSyncCheckpoint(dir, threadID string, seen map[string][32]byte) error {
	data, err := json.Marshal(codexSyncCheckpoint{ThreadID: threadID, Seen: seen})
	if err != nil {
		return err
	}
	return atomicfile.Write(filepath.Join(dir, codexSyncFile), data)
}

func codexTurnFingerprint(raw codex.Raw) [32]byte {
	// The projection is part of the fingerprint: unchanged Codex turns must
	// be rebuilt when an adapter fix changes the transcript rows they produce.
	encoded, _ := json.Marshal(struct {
		Version int
		Turn    codex.Raw
	}{Version: 1, Turn: raw})
	return sha256.Sum256(encoded)
}

// SyncSession refreshes a linked agent's conversation. A running Paladin turn
// owns its transcript and is left to its live event stream.
func (r *AgentRuntime) SyncSession(sessionID, cwd string) (bool, error) {
	if r.hub.Running(sessionID) {
		return false, nil
	}
	return r.syncCodexSession(sessionID, cwd, false)
}

func (r *AgentRuntime) syncCodexSession(sessionID, cwd string, ownTurn bool) (bool, error) {
	sess, err := r.transcripts.Load(cwd, sessionID)
	if err != nil {
		return false, err
	}
	h := sess.Meta.Harness
	if h == nil || h.Name != HarnessCodex || h.ThreadID == "" {
		return false, nil
	}
	v, _ := r.codexSync.LoadOrStore(sessionID, &codexSyncState{})
	st := v.(*codexSyncState)
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.threadID == "" {
		data, err := os.ReadFile(filepath.Join(sess.Dir, codexSyncFile))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
		if err == nil {
			var saved codexSyncCheckpoint
			if err := json.Unmarshal(data, &saved); err != nil {
				return false, err
			}
			st.threadID, st.seen = saved.ThreadID, saved.Seen
		}
	}
	if !ownTurn && r.hub.Running(sessionID) {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(r.sideCtx, 15*time.Second)
	defer cancel()
	client, err := r.codex.Client(ctx)
	if err != nil {
		return false, err
	}
	latest, err := client.LatestTurn(ctx, h.ThreadID)
	if err != nil {
		return false, err
	}
	encoded, err := json.Marshal(latest)
	if err != nil {
		return false, err
	}
	probe := sha256.Sum256(encoded)
	if st.threadID == h.ThreadID && st.latest == probe && time.Since(st.checked) < 30*time.Second {
		return false, nil
	}
	source, err := client.ThreadTurns(ctx, h.ThreadID)
	if err != nil {
		return false, err
	}
	changed := false
	r.undoMu.Lock()
	defer r.undoMu.Unlock()
	err = r.hub.Gated(sessionID, func() error {
		if !ownTurn && r.hub.Running(sessionID) {
			return nil
		}
		// Reload inside the gate: undo, deletion or another turn may have
		// changed the session while Codex was answering the read.
		current, err := r.transcripts.Load(cwd, sessionID)
		if err != nil {
			return err
		}
		if current.Meta.Harness == nil || current.Meta.Harness.ThreadID != h.ThreadID || current.Meta.Revert != nil {
			return nil
		}
		if !ownTurn {
			if _, err := current.RecoverInterrupted(); err != nil {
				return err
			}
		}
		local, err := current.HistoryTurns()
		if err != nil {
			return err
		}
		if st.threadID != h.ThreadID {
			st.seen = nil
		}
		next, seen := reconcileCodexTurns(local, source, st.seen)
		if !reflect.DeepEqual(local, next) {
			if err := current.ReplaceTurns(next); err != nil {
				return err
			}
			changed = true
		}
		if st.threadID != h.ThreadID || !reflect.DeepEqual(st.seen, seen) {
			if err := saveCodexSyncCheckpoint(current.Dir, h.ThreadID, seen); err != nil {
				return err
			}
		}
		st.threadID, st.seen, st.latest = h.ThreadID, seen, probe
		st.checked = time.Now()
		return nil
	})
	return changed, err
}

func harnessTurnID(turn transcript.Turn) string {
	for _, m := range turn.Messages {
		if m.HarnessTurn != "" {
			return m.HarnessTurn
		}
	}
	return ""
}

// Reuse unchanged local turns, keeping Paladin's richer live attachments,
// statistics and timestamps. Changed external turns replace their projection;
// new external turns are inserted in Codex order, once per turn id.
func reconcileCodexTurns(local []transcript.Turn, source []codex.Raw, previous map[string][32]byte) ([]transcript.Turn, map[string][32]byte) {
	byID := map[string]transcript.Turn{}
	tails := map[string][]transcript.Turn{}
	anchor := ""
	for _, turn := range local {
		if id := harnessTurnID(turn); id != "" {
			byID[id], anchor = turn, id
		} else {
			tails[anchor] = append(tails[anchor], turn)
		}
	}
	out := append([]transcript.Turn{}, tails[""]...)
	seen := map[string][32]byte{}
	for _, raw := range source {
		id := raw.Str("id")
		if id == "" {
			continue
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		hash := codexTurnFingerprint(raw)
		old, exists := byID[id]
		if before, known := previous[id]; known && before == hash && exists {
			out = append(out, old)
		} else if turn := mirroredCodexTurn(raw, old); len(turn.Messages) > 0 {
			out = append(out, turn)
		}
		out = append(out, tails[id]...)
		seen[id] = hash
	}
	for _, turn := range local {
		id := harnessTurnID(turn)
		if id == "" {
			continue
		}
		if _, found := seen[id]; found {
			continue
		}
		// Codex can take a moment to flush a just-finished local turn. Keep
		// it until Codex has acknowledged it; a later external rewind can
		// remove turns that the source previously contained.
		if _, acknowledged := previous[id]; !acknowledged {
			out = append(out, turn)
		}
		out = append(out, tails[id]...)
	}
	return out, seen
}

func mirroredCodexTurn(raw codex.Raw, old transcript.Turn) transcript.Turn {
	msgs := codexTurnMessages(raw)
	if len(msgs) == 0 {
		return transcript.Turn{}
	}
	// Codex stores a /compact turn's checkpoint, but not the user's slash
	// command. Retain Paladin's local command instead of erasing it on sync.
	if len(old.Messages) > 0 && old.Messages[0].Role == transcript.RoleUser && strings.TrimSpace(old.Messages[0].Content) == "/compact" {
		compacted, hasCommand := false, false
		for i := range msgs {
			hasCommand = hasCommand || (msgs[i].Role == transcript.RoleUser && strings.TrimSpace(msgs[i].Content) == "/compact")
			if msgs[i].IsCompaction() {
				compacted = true
				msgs[i].Compaction.Reason = "manual"
			}
		}
		if compacted && !hasCommand {
			msgs = append([]transcript.Message{old.Messages[0]}, msgs...)
		}
	}
	msgs[0].HarnessTurn = raw.Str("id")
	at := time.Unix(raw.Int("startedAt"), 0).UTC()
	if raw.Int("startedAt") <= 0 && len(old.Messages) > 0 {
		at = old.Messages[0].CreatedAt
	}
	users := map[string][]transcript.Message{}
	for _, kept := range old.Messages {
		if kept.Role == transcript.RoleUser {
			users[kept.Content] = append(users[kept.Content], kept)
		}
	}
	for i := range msgs {
		msgs[i].CreatedAt = at
		if kept := users[msgs[i].Content]; msgs[i].Role == transcript.RoleUser && len(kept) > 0 {
			msgs[i].Attachments, msgs[i].Display = kept[0].Attachments, kept[0].Display
			users[msgs[i].Content] = kept[1:]
		}
	}
	stats := old.Stats
	if stats == nil && raw.Int("durationMs") > 0 {
		stats = &transcript.TurnStats{DurationMs: raw.Int("durationMs")}
	}
	return transcript.Turn{Messages: msgs, Stats: stats}
}
