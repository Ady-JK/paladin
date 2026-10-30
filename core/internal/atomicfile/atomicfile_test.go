package atomicfile

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWriteReplacesContentWithOwnerOnlyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, []byte("new")); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "new" {
		t.Fatalf("content = %q, %v", got, err)
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(path)
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("perm = %o, want 600", perm)
		}
	}
}

func TestWriteReportsRenameFailureAndCleansUp(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "state.json")
	// A non-empty directory at the target makes the final rename fail.
	if err := os.MkdirAll(filepath.Join(target, "occupied"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Write(target, []byte("{}")); err == nil {
		t.Fatal("Write returned nil although the rename failed")
	}
	assertNoTempFiles(t, dir)
}

func TestWriteFailsWhenTheFolderIsMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "state.json")
	if err := Write(path, []byte("x")); err == nil {
		t.Fatal("Write into a missing folder succeeded")
	}
}

func assertNoTempFiles(t *testing.T, dir string) {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}
