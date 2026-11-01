package permission

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeIn(t *testing.T, ws, rel string) Request {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"path": filepath.Join(ws, rel)})
	r, ok := Classify("write", raw, ws)
	if !ok {
		t.Fatalf("not gated: %s", rel)
	}
	return r
}

// Files that make something run later (git hooks and config, agent
// settings, installed tools) ask before every change, in every preset
// but full access, unless the user allowed that folder for the session.
func TestWritesToProtectedFoldersAsk(t *testing.T) {
	ws := t.TempDir()
	os.MkdirAll(filepath.Join(ws, "venv"), 0o755)
	os.WriteFile(filepath.Join(ws, "venv", "pyvenv.cfg"), []byte("home = /usr\n"), 0o644)
	for _, rel := range []string{
		".git/config", ".git/hooks/pre-commit", ".git", "sub/.git/config", ".GIT/config",
		".paladin/hooks.json", ".paladin/mcp.json", ".claude/settings.json",
		"node_modules/.bin/prettier", "node_modules/prettier/index.js", ".venv/bin/ruff", "venv/bin/black",
	} {
		d := Decide(Auto, writeIn(t, ws, rel), nil)
		if d.Verdict != Ask || len(d.Reasons) == 0 || len(d.Choices) != 3 || d.Choices[2] != ReplyDeny {
			t.Errorf("%s: %+v", rel, d)
		}
		if d := Decide(FullAccess, writeIn(t, ws, rel), nil); d.Verdict != Allow {
			t.Errorf("full access asks for %s", rel)
		}
	}
	for _, rel := range []string{"src/main.go", ".github/workflows/ci.yml", "venv2/x.py", "docs/node_modules.md", "gitconfig"} {
		if d := Decide(Auto, writeIn(t, ws, rel), nil); d.Verdict != Allow {
			t.Errorf("%s asks: %+v", rel, d)
		}
	}
	// The answer remembers only that folder, and only for the session.
	d := Decide(Auto, writeIn(t, ws, ".git/config"), nil)
	if len(d.Remember) != 1 || d.Remember[0].Pattern != filepath.Join(ws, ".git") {
		t.Fatalf("remember %+v", d.Remember)
	}
	session := []Rule{{Tool: ToolWrite, Pattern: filepath.Join(ws, ".git"), Action: ActionAllow}}
	if d := Decide(Auto, writeIn(t, ws, ".git/hooks/x"), session); d.Verdict != Allow {
		t.Fatalf("an allowed protected folder: %+v", d)
	}
	// A broader rule (the whole workspace, or everything) does not cover
	// a protected folder inside it.
	for _, broad := range []string{ws, "*"} {
		rules := []Rule{{Tool: ToolWrite, Pattern: broad, Action: ActionAllow}}
		if d := Decide(Auto, writeIn(t, ws, ".claude/settings.json"), rules); d.Verdict != Ask {
			t.Fatalf("rule %q covered a protected folder: %+v", broad, d)
		}
	}
}

// A symlink into a protected folder is that folder.
func TestProtectedFolderThroughASymlink(t *testing.T) {
	ws := t.TempDir()
	os.MkdirAll(filepath.Join(ws, ".git", "hooks"), 0o755)
	if err := os.Symlink(filepath.Join(ws, ".git", "hooks"), filepath.Join(ws, "hooks-link")); err != nil {
		t.Skip(err)
	}
	if d := Decide(Auto, writeIn(t, ws, "hooks-link/pre-commit"), nil); d.Verdict != Ask {
		t.Fatalf("through a symlink: %+v", d)
	}
}
