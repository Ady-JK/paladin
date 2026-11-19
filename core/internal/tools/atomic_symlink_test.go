package tools

import (
	"os"
	"path/filepath"
	"testing"
)

// A write must never land outside the folder it was aimed at, even when a
// parent directory is swapped for a symlink after the temp file exists:
// the temp file and the destination share one path, so the swap makes the
// final rename fail instead of redirecting it.
func TestAtomicReplaceSurvivesAParentSwappedForASymlink(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "ws", "dir")
	outside := filepath.Join(root, "outside")
	for _, d := range []string{inside, outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	target := filepath.Join(inside, "file.txt")

	prev := beforeCommit
	t.Cleanup(func() { beforeCommit = prev })
	beforeCommit = func(string) {
		if err := os.Rename(inside, inside+".moved"); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, inside); err != nil {
			t.Fatal(err)
		}
	}

	if err := atomicReplace(target, []byte("payload"), 0o644, nil); err == nil {
		t.Fatal("rename succeeded through a swapped parent")
	}
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("something was written outside the folder: %v", entries)
	}
}
