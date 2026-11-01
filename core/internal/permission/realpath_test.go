package permission

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeOutside(t *testing.T, path string) Request {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"path": path})
	r, ok := Classify("write", raw, t.TempDir())
	if !ok {
		t.Fatalf("not gated: %s", path)
	}
	return r
}

// An allow rule covers a write only where the path really leads: a
// symlink inside an allowed folder does not carry the grant out, and a
// deny rule stops a write through a symlink too.
func TestWriteRulesFollowSymlinks(t *testing.T) {
	base := t.TempDir()
	granted, secret := filepath.Join(base, "granted"), filepath.Join(base, "secret")
	os.MkdirAll(granted, 0o755)
	os.MkdirAll(secret, 0o755)
	if err := os.Symlink(secret, filepath.Join(granted, "link")); err != nil {
		t.Skip(err)
	}
	req := writeOutside(t, filepath.Join(granted, "link", "authorized_keys"))
	allow := []Rule{{Tool: ToolWrite, Pattern: granted, Action: ActionAllow}}
	if d := Decide(Auto, req, allow); d.Verdict == Allow {
		t.Fatalf("the grant followed a symlink out: %+v", d)
	}
	deny := []Rule{{Tool: ToolWrite, Pattern: secret, Action: ActionDeny}}
	if d := Decide(FullAccess, req, deny); d.Verdict != Deny {
		t.Fatalf("a deny rule missed the symlinked path: %+v", d)
	}
	if d := Decide(Auto, writeOutside(t, filepath.Join(granted, "plain.txt")), allow); d.Verdict != Allow {
		t.Fatalf("a plain path in the granted folder: %+v", d)
	}
}

// On case-insensitive file systems (macOS, Windows) a different case
// names the same folder, so rules compare case-blind there.
func TestPathRulesIgnoreCaseWhereTheFileSystemDoes(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("case-sensitive file system")
	}
	home := t.TempDir()
	deny := []Rule{{Tool: ToolWrite, Pattern: filepath.Join(home, ".ssh"), Action: ActionDeny}}
	req := writeOutside(t, filepath.Join(home, strings.ToUpper(".ssh"), "authorized_keys"))
	if d := Decide(FullAccess, req, deny); d.Verdict != Deny {
		t.Fatalf("~/.SSH slipped past a deny rule for ~/.ssh: %+v", d)
	}
}
