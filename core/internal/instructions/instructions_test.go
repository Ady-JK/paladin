package instructions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestHierarchyRootToCwd(t *testing.T) {
	repo := t.TempDir()
	_ = os.Mkdir(filepath.Join(repo, ".git"), 0o755)
	write(t, filepath.Join(repo, "AGENTS.md"), "root rules")
	write(t, filepath.Join(repo, "svc", "AGENTS.md"), "svc rules")
	cwd := filepath.Join(repo, "svc", "api")
	_ = os.MkdirAll(cwd, 0o755)
	write(t, filepath.Join(cwd, "AGENTS.override.md"), "api override")
	write(t, filepath.Join(cwd, "AGENTS.md"), "api plain")

	docs := Discover("", cwd)
	if len(docs) != 3 {
		t.Fatalf("docs = %+v", docs)
	}
	if docs[0].Content != "root rules" || docs[1].Content != "svc rules" || docs[2].Content != "api override" {
		t.Fatalf("order/override wrong: %+v", docs)
	}
	text := Render(docs)
	if strings.Index(text, "root rules") > strings.Index(text, "api override") {
		t.Fatal("root must come first")
	}
	if strings.Contains(text, "api plain") {
		t.Fatal("override should shadow AGENTS.md")
	}
}

func TestNoGitOnlyCwd(t *testing.T) {
	parent := t.TempDir()
	write(t, filepath.Join(parent, "AGENTS.md"), "parent")
	cwd := filepath.Join(parent, "child")
	write(t, filepath.Join(cwd, "AGENTS.md"), "child")
	docs := Discover("", cwd)
	if len(docs) != 1 || docs[0].Content != "child" {
		t.Fatalf("docs = %+v", docs)
	}
}

func TestClaudeFallbackAndGlobal(t *testing.T) {
	data := t.TempDir()
	write(t, filepath.Join(data, "config", "AGENTS.md"), "global rules")
	cwd := t.TempDir()
	write(t, filepath.Join(cwd, "CLAUDE.md"), "claude rules")
	docs := Discover(data, cwd)
	if len(docs) != 2 || docs[0].Scope != "global" || docs[1].Content != "claude rules" {
		t.Fatalf("docs = %+v", docs)
	}
}

func TestSizeCap(t *testing.T) {
	cwd := t.TempDir()
	write(t, filepath.Join(cwd, "AGENTS.md"), strings.Repeat("x", MaxBytes*2))
	docs := Discover("", cwd)
	if len(docs) != 1 || len(docs[0].Content) > MaxBytes+100 || !docs[0].Truncated {
		t.Fatalf("cap not applied: len=%d", len(docs[0].Content))
	}
}

func TestRenderEmpty(t *testing.T) {
	if Render(nil) != "" {
		t.Fatal("empty should render blank")
	}
}
