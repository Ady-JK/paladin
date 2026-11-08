package tools

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestIDESearchFindsLiteralLinesAndSkipsIgnored(t *testing.T) {
	root := t.TempDir()
	if err := exec.Command("git", "init", "-q", root).Run(); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{".gitignore": "ignored.txt\n", "main.go": "first\nA literal [text] here\nlast\n", "ignored.txt": "literal [text]\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	result, err := IDESearch(root, "literal [text]")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Hits) != 1 || result.Hits[0].Path != filepath.Join(root, "main.go") || result.Hits[0].Line != 2 {
		t.Fatalf("unexpected search result: %+v", result)
	}
}

func TestIDESearchRejectsInvalidWorkspaceOrQuery(t *testing.T) {
	for _, tc := range []struct{ root, query string }{{"relative", "x"}, {t.TempDir(), ""}} {
		if _, err := IDESearch(tc.root, tc.query); err == nil {
			t.Fatalf("expected rejection for %q, %q", tc.root, tc.query)
		}
	}
}

func TestIDESearchIgnoresFilesOutsideGitWithoutRipgrep(t *testing.T) {
	old := searchUseRipgrep
	searchUseRipgrep = false
	defer func() { searchUseRipgrep = old }()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("secret.txt\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "secret.txt"), []byte("needle"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := IDESearch(root, "needle")
	if err != nil || len(result.Hits) != 0 {
		t.Fatalf("ignored file was searched: %+v, %v", result, err)
	}
}
