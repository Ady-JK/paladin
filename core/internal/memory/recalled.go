// recalled.go remembers which memory entries a session already has in
// its conversation, so a recall (see runtime.memoryRecall) is appended
// once and not again on every later message that happens to match it.
//
// The record is a set of hashes beside the transcript. It describes what
// the conversation holds, so it is rebuilt whenever that changes: a
// compaction replaces the old messages with a summary (the kept tail
// still holds its reminders), and an undo truncates them.

package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Ady-JK/paladin/core/internal/atomicfile"
)

// RecalledFile holds a session's recalled-entry hashes.
const RecalledFile = "memory_recalled.json"

// maxRecalled bounds the file; a session cannot recall more distinct
// entries than its stores hold, so this is only a safety cap.
const maxRecalled = 256

type recalledState struct {
	Hashes []string `json:"hashes"`
}

// RecallHash identifies one recalled entry: the store it came from and
// its text with whitespace folded, so a reflowed entry is still the same
// one while an edited entry is a new one.
func RecallHash(target Target, entry string) string {
	sum := sha256.Sum256([]byte(string(target) + "\x00" + strings.Join(strings.Fields(entry), " ")))
	return hex.EncodeToString(sum[:12])
}

func recalledPath(sessionDir string) string { return filepath.Join(sessionDir, RecalledFile) }

func loadRecalled(sessionDir string) recalledState {
	var st recalledState
	data, err := os.ReadFile(recalledPath(sessionDir))
	if err != nil || json.Unmarshal(data, &st) != nil {
		// A missing or damaged record means nothing is known to be in
		// the conversation, so the entry is recalled again.
		return recalledState{}
	}
	return st
}

// IsRecalled reports whether the session already holds this entry.
func IsRecalled(sessionDir, hash string) bool {
	if strings.TrimSpace(sessionDir) == "" || hash == "" {
		return false
	}
	return slices.Contains(loadRecalled(sessionDir).Hashes, hash)
}

// MarkRecalled records that the session's conversation now holds the entry.
func MarkRecalled(sessionDir, hash string) error {
	if strings.TrimSpace(sessionDir) == "" || hash == "" {
		return nil
	}
	defer reviewLock(recalledPath(sessionDir))()
	st := loadRecalled(sessionDir)
	if slices.Contains(st.Hashes, hash) {
		return nil
	}
	st.Hashes = append(st.Hashes, hash)
	if len(st.Hashes) > maxRecalled {
		st.Hashes = st.Hashes[len(st.Hashes)-maxRecalled:]
	}
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return atomicfile.Write(recalledPath(sessionDir), data)
}

// recallNoteMid sits between the store name and the entry in a recall
// reminder; RecallNote builds one and RecalledIn reads them back.
const recallNoteMid = " memory — this request looks related:\n"

// RecallNote is the reminder that puts an entry in front of the model.
func RecallNote(target Target, entry string) string {
	return "Recalled from " + string(target) + recallNoteMid + entry
}

// RecalledIn returns the hashes of the recall reminders among contents
// (the messages a conversation holds), so the record can be rebuilt from
// what is really there.
func RecalledIn(contents []string) []string {
	var out []string
	for _, c := range contents {
		rest, ok := strings.CutPrefix(c, "Recalled from ")
		if !ok {
			continue
		}
		name, entry, ok := strings.Cut(rest, recallNoteMid)
		if !ok {
			continue
		}
		target, err := ParseTarget(name)
		if err != nil {
			continue
		}
		if hash := RecallHash(target, entry); !slices.Contains(out, hash) {
			out = append(out, hash)
		}
	}
	return out
}

// ResetRecalled replaces the record with exactly these hashes; none
// removes it. A compaction or an undo uses it to keep the entries whose
// reminders are still in the conversation and forget the rest, which the
// next matching message recalls afresh.
func ResetRecalled(sessionDir string, hashes []string) error {
	if strings.TrimSpace(sessionDir) == "" {
		return nil
	}
	defer reviewLock(recalledPath(sessionDir))()
	if len(hashes) == 0 {
		if err := os.Remove(recalledPath(sessionDir)); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if len(hashes) > maxRecalled {
		hashes = hashes[len(hashes)-maxRecalled:]
	}
	data, err := json.Marshal(recalledState{Hashes: hashes})
	if err != nil {
		return err
	}
	return atomicfile.Write(recalledPath(sessionDir), data)
}
