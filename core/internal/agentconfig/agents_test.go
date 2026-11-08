package agentconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/config"
)

func TestDirectoryUsesUserConfigAndIsolatesDataOverrides(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PALADIN_DATA_DIR", "")
	t.Setenv("PALADIN_AGENTS_DIR", "")
	got, err := Dir(config.DataDir())
	if err != nil || got != filepath.Join(home, ".config", "paladin", "agents") {
		t.Fatalf("default dir = %q, %v", got, err)
	}
	data := t.TempDir()
	got, err = Dir(data)
	if err != nil || got != filepath.Join(data, "agents") {
		t.Fatalf("isolated dir = %q, %v", got, err)
	}
	t.Setenv("PALADIN_AGENTS_DIR", "relative")
	if _, err := Dir(data); err == nil {
		t.Fatal("accepted relative override")
	}
}

func TestListValidatesAgentFilesAndReloads(t *testing.T) {
	root := t.TempDir()
	write := func(name, text string) {
		t.Helper()
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "agent.json"), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("scout", `{"name":"scout","description":"Search","permissions":"read-only","tools":{"read":true},"mcp":{"local":["lookup"]},"role":"Find facts"}`)
	write("broken", `{"name":"broken","description":"bad","permissions":"god","tools":{},"mcp":{},"role":"x"}`)
	found, errs := List(root)
	if len(found) != 1 || found[0].Name != "scout" || len(errs) != 1 {
		t.Fatalf("found=%+v errors=%v", found, errs)
	}
	write("scout", `{"name":"scout","description":"Updated","permissions":"read-write","tools":{"edit":true},"mcp":{},"role":"Fix bugs"}`)
	found, _ = List(root)
	if found[0].Permissions != "read-write" || !found[0].Tools["edit"] || !strings.Contains(found[0].Role, "Fix") {
		t.Fatalf("stale: %+v", found)
	}
	write("bad-name", `{"name":"other","description":"bad","permissions":"read-only","tools":{},"mcp":{},"role":"x"}`)
	_, errs = List(root)
	if len(errs) != 2 {
		t.Fatalf("expected bad filename rejected: %v", errs)
	}
}

func TestListRejectsInvalidOrUnsafeFiles(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"oversized", "extra", "linked"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "oversized", FileName), []byte(strings.Repeat("x", maxBytes+1)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "extra", FileName), []byte(`{"name":"extra","description":"test","permissions":"read-only","tools":{},"mcp":{},"role":"x"} {}`), 0600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "extra", FileName)
	if err := os.Symlink(target, filepath.Join(root, "linked", FileName)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	found, failures := List(root)
	if len(found) != 0 || len(failures) != 3 {
		t.Fatalf("found=%v failures=%v", found, failures)
	}
}
