package runtime

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/Ady-JK/paladin/core/internal/atomicfile"
	"github.com/Ady-JK/paladin/core/internal/session"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

const protectionFile = "snapshot-protection.json"

func readProtection(dir string) ([]session.SnapshotProtection, error) {
	data, err := os.ReadFile(filepath.Join(dir, protectionFile))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var entries []session.SnapshotProtection
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

func writeProtection(dir string, entries []session.SnapshotProtection) error {
	data, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	return atomicfile.Write(filepath.Join(dir, protectionFile), data)
}

func saveProtection(dir string, value session.SnapshotProtection) error {
	entries, err := readProtection(dir)
	if err != nil {
		return err
	}
	floor, err := undoFloor(dir)
	if err != nil {
		return err
	}
	kept := make([]session.SnapshotProtection, 0, maxUndoTurns)
	for _, entry := range entries {
		if entry.Turn >= floor && entry.Turn != value.Turn {
			kept = append(kept, entry)
		}
	}
	kept = append(kept, value)
	sort.Slice(kept, func(i, j int) bool { return kept[i].Turn < kept[j].Turn })
	if len(kept) > maxUndoTurns {
		kept = kept[len(kept)-maxUndoTurns:]
	}
	return writeProtection(dir, kept)
}

func annotateProtection(sess transcript.Session, rows []session.HistoryMessage, turns []transcript.Turn) error {
	entries, err := readProtection(sess.Dir)
	if err != nil {
		return fmt.Errorf("read snapshot status: %w", err)
	}
	floor, err := undoFloor(sess.Dir)
	if err != nil {
		return err
	}
	byTurn := make(map[int]session.SnapshotProtection, len(entries))
	for _, entry := range entries {
		byTurn[entry.Turn] = entry
	}
	snapshots, err := readTurnSnapshots(sess.Dir)
	if err != nil {
		return err
	}
	for _, snap := range snapshots {
		if _, ok := byTurn[snap.Turn]; !ok {
			byTurn[snap.Turn] = session.SnapshotProtection{Turn: snap.Turn, State: "partial", Reason: "Snapshot saved before exclusion details were recorded.", Truncated: true}
		}
	}
	starts := map[int]bool{}
	pos := 0
	for _, turn := range turns {
		if len(turn.Messages) > 0 && turn.Messages[0].Role == transcript.RoleUser && !turn.Messages[0].Replay && !session.IsNotifyMessage(turn.Messages[0]) {
			starts[pos] = true
		}
		pos += len(turn.Messages)
	}
	for i := range rows {
		row := &rows[i]
		if row.Role != "user" || row.Index == nil || !starts[*row.Index] {
			continue
		}
		if sess.Meta.Subagent != nil {
			row.Protection = &session.SnapshotProtection{Turn: *row.Index, State: "unavailable", Reason: "Subagent turns do not create their own snapshots."}
		} else if *row.Index < floor {
			row.Protection = &session.SnapshotProtection{Turn: *row.Index, State: "expired", Reason: "Only the last three turns can be undone."}
		} else if status, ok := byTurn[*row.Index]; ok {
			copy := status
			row.Protection = &copy
		} else {
			row.Protection = &session.SnapshotProtection{Turn: *row.Index, State: "unknown", Reason: "Snapshot details are unavailable for this turn."}
		}
	}
	return nil
}

func markProtectionUnknown(rows []session.HistoryMessage) {
	for i := range rows {
		if rows[i].Role == "user" && rows[i].Index != nil {
			rows[i].Protection = &session.SnapshotProtection{Turn: *rows[i].Index, State: "unknown", Reason: "Could not read snapshot protection details."}
		}
	}
}

func pruneProtection(dir string, keepBefore int) error {
	entries, err := readProtection(dir)
	if err != nil {
		return err
	}
	kept := entries[:0]
	for _, entry := range entries {
		if entry.Turn < keepBefore {
			kept = append(kept, entry)
		}
	}
	return writeProtection(dir, kept)
}
