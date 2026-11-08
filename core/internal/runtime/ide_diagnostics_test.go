package runtime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIDEDiagnoseRejectsOutsideFilesAndReportsNoServer(t *testing.T) {
	rt, _ := testRuntime(t)
	ws, other := t.TempDir(), t.TempDir()
	file := filepath.Join(ws, "notes.paladin-unknown")
	outside := filepath.Join(other, "notes.paladin-unknown")
	for _, path := range []string{file, outside} {
		if err := os.WriteFile(path, []byte("hello"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := rt.IDEDiagnose(ws, outside, "draft"); err == nil {
		t.Fatal("outside path accepted")
	}
	result, err := rt.IDEDiagnose(ws, file, "draft")
	if err != nil || result.State != "unavailable" || len(result.Diagnostics) != 0 {
		t.Fatalf("result %+v, %v", result, err)
	}
}
