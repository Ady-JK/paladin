package runtime

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func BenchmarkIDEList(b *testing.B) {
	root := b.TempDir()
	if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		b.Fatalf("git init: %s: %v", out, err)
	}
	for i := 0; i < 2000; i++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("file-%04d.txt", i)), nil, 0600); err != nil {
			b.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("*.log\n"), 0600); err != nil {
		b.Fatal(err)
	}
	rt := &AgentRuntime{}
	settings := IDEExplorerSettings{HideIgnored: true}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := rt.IDEList(root, root, settings); err != nil {
			b.Fatal(err)
		}
	}
}
