package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestIDEFileStatusDetectsChangedAndMissingFiles(t *testing.T) {
	rt, _ := testRuntime(t)
	ws := t.TempDir()
	path := filepath.Join(ws, "open.txt")
	if err := os.WriteFile(path, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	file, err := IDEReadFile(ws, path)
	if err != nil {
		t.Fatal(err)
	}
	input := []IDEFileCheck{{Path: path, Version: file.Version}}
	check := func(want string) {
		t.Helper()
		got, err := rt.IDEFileStatuses(ws, input)
		if err != nil || len(got) != 1 || got[0].State != want {
			t.Fatalf("statuses=%+v error=%v, want %s", got, err, want)
		}
	}
	check("same")
	if err := os.WriteFile(path, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	check("changed")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	check("unavailable")
}

func BenchmarkIDEOpenFileChecks(b *testing.B) {
	rt := &AgentRuntime{}
	ws := b.TempDir()
	checks := make([]IDEFileCheck, 0, 100)
	content := make([]byte, 64<<10)
	for i := range content {
		content[i] = 'a'
	}
	for i := range 100 {
		path := filepath.Join(ws, fmt.Sprintf("file-%03d.txt", i))
		if err := os.WriteFile(path, content, 0600); err != nil {
			b.Fatal(err)
		}
		file, err := IDEReadFile(ws, path)
		if err != nil {
			b.Fatal(err)
		}
		checks = append(checks, IDEFileCheck{Path: path, Version: file.Version})
	}
	if _, err := rt.IDEFileStatuses(ws, checks); err != nil {
		b.Fatal(err)
	}
	b.Run("cached_metadata", func(b *testing.B) {
		for range b.N {
			if _, err := rt.IDEFileStatuses(ws, checks); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("full_reads", func(b *testing.B) {
		for range b.N {
			for _, check := range checks {
				if _, err := IDEReadFile(ws, check.Path); err != nil {
					b.Fatal(err)
				}
			}
		}
	})
}
