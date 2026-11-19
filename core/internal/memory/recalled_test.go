package memory

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRecallHashFoldsWhitespaceButNotTextOrStore(t *testing.T) {
	base := RecallHash(TargetProject, "use pnpm\nworkspaces")
	if RecallHash(TargetProject, "  use   pnpm workspaces ") != base {
		t.Fatal("whitespace changed the hash")
	}
	if RecallHash(TargetProject, "use npm workspaces") == base {
		t.Fatal("different text shares a hash")
	}
	if RecallHash(TargetGlobal, "use pnpm workspaces") == base {
		t.Fatal("the same text in another store shares a hash")
	}
}

func TestRecalledEntriesAreRememberedUntilCleared(t *testing.T) {
	dir := t.TempDir()
	hash := RecallHash(TargetUser, "likes terse answers")
	if IsRecalled(dir, hash) {
		t.Fatal("a fresh session already recalled an entry")
	}
	for i := 0; i < 2; i++ { // marking twice stores it once
		if err := MarkRecalled(dir, hash); err != nil {
			t.Fatal(err)
		}
	}
	if !IsRecalled(dir, hash) || IsRecalled(dir, RecallHash(TargetUser, "other")) {
		t.Fatal("recalled state is wrong after marking")
	}
	if got := loadRecalled(dir).Hashes; len(got) != 1 {
		t.Fatalf("hashes = %v", got)
	}
	if err := ResetRecalled(dir, nil); err != nil {
		t.Fatal(err)
	}
	if IsRecalled(dir, hash) {
		t.Fatal("reset to nothing kept the hash")
	}
	if err := ResetRecalled(dir, nil); err != nil {
		t.Fatalf("resetting nothing failed: %v", err)
	}
}

func TestResetRecalledReplacesTheRecord(t *testing.T) {
	dir := t.TempDir()
	old, kept := RecallHash(TargetProject, "old entry"), RecallHash(TargetProject, "kept entry")
	if err := MarkRecalled(dir, old); err != nil {
		t.Fatal(err)
	}
	if err := ResetRecalled(dir, []string{kept}); err != nil {
		t.Fatal(err)
	}
	if IsRecalled(dir, old) || !IsRecalled(dir, kept) {
		t.Fatalf("record after reset = %v", loadRecalled(dir).Hashes)
	}
}

func TestRecalledInReadsBackTheReminders(t *testing.T) {
	entry := "tests run with make test"
	got := RecalledIn([]string{
		"just a user message",
		RecallNote(TargetProject, entry),
		RecallNote(TargetProject, entry), // the same entry twice counts once
		RecallNote(TargetUser, "likes terse answers"),
		"Recalled from nowhere memory — this request looks related:\nx", // unknown store
		"Recalled from project memory but not in the reminder's shape",
	})
	want := []string{RecallHash(TargetProject, entry), RecallHash(TargetUser, "likes terse answers")}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("RecalledIn = %v, want %v", got, want)
	}
}

func TestDamagedRecalledFileMeansNothingIsRecalled(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, RecalledFile), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if IsRecalled(dir, "abc") {
		t.Fatal("a damaged file claimed an entry was recalled")
	}
	if err := MarkRecalled(dir, "abc"); err != nil || !IsRecalled(dir, "abc") {
		t.Fatalf("a damaged file blocked marking: %v", err)
	}
}

func TestRecalledWithoutASessionDirIsInert(t *testing.T) {
	if IsRecalled("", "x") || MarkRecalled("", "x") != nil || ResetRecalled("", nil) != nil {
		t.Fatal("an empty session dir should do nothing")
	}
}
