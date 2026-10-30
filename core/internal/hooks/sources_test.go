package hooks

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const stopHook = `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo hi"}]}]}}`

func TestGlobalHooksAreTrustedAndActive(t *testing.T) {
	data := t.TempDir()
	write(t, GlobalPath(data), stopHook)
	m := NewManager(data)
	srcs := m.List("")
	if len(srcs) != 1 || srcs[0].Scope != ScopeGlobal || !srcs[0].Trusted || len(srcs[0].Handlers) != 1 {
		t.Fatalf("sources = %+v", srcs)
	}
	if !m.Engine("").Has(Stop) {
		t.Fatal("global hook should be active")
	}
}

func TestFolderHooksNeedTrustPinnedToContent(t *testing.T) {
	data, folder := t.TempDir(), t.TempDir()
	path := filepath.Join(folder, ".claude", "settings.json")
	write(t, path, stopHook)
	m := NewManager(data)
	var claude Source
	for _, s := range m.List(folder) {
		if s.Path == path {
			claude = s
		}
	}
	if claude.Path == "" || claude.Trusted || claude.Format != "claude" {
		t.Fatalf("claude = %+v", claude)
	}
	if m.Engine(folder).Has(Stop) {
		t.Fatal("untrusted folder hook must not run")
	}
	if err := m.Trust(folder, path, true); err != nil {
		t.Fatal(err)
	}
	if !m.Engine(folder).Has(Stop) {
		t.Fatal("trusted folder hook should run")
	}
	write(t, path, `{"hooks":{"Stop":[{"hooks":[{"command":"rm -rf /"}]}]}}`)
	if m.Engine(folder).Has(Stop) {
		t.Fatal("edited folder hook must need trust again")
	}
}

func TestTrustRejectsUnknownPaths(t *testing.T) {
	m := NewManager(t.TempDir())
	if err := m.Trust(t.TempDir(), "/etc/passwd", true); err == nil {
		t.Fatal("want error")
	}
}

func TestAllThreeFolderFormatsAreRead(t *testing.T) {
	data, folder := t.TempDir(), t.TempDir()
	write(t, FolderPath(folder), stopHook)
	write(t, filepath.Join(folder, ".codex", "hooks.json"), stopHook)
	write(t, filepath.Join(folder, ".claude", "settings.json"), stopHook)
	formats := map[string]bool{}
	for _, s := range NewManager(data).List(folder) {
		if s.Exists {
			formats[s.Format] = true
		}
	}
	for _, f := range []string{"paladin", "codex", "claude"} {
		if !formats[f] {
			t.Fatalf("missing %s in %v", f, formats)
		}
	}
}

func TestMissingFilesAreListedButEmpty(t *testing.T) {
	srcs := NewManager(t.TempDir()).List(t.TempDir())
	if len(srcs) != 4 {
		t.Fatalf("want global + three folder sources, got %+v", srcs)
	}
	for _, s := range srcs {
		if s.Exists || len(s.Handlers) != 0 {
			t.Fatalf("source = %+v", s)
		}
	}
}

func TestToggleDisablesOneHandler(t *testing.T) {
	data := t.TempDir()
	write(t, GlobalPath(data), stopHook)
	m := NewManager(data)
	key := m.List("")[0].Handlers[0].Key
	if err := m.Toggle(key, false); err != nil {
		t.Fatal(err)
	}
	if m.List("")[0].Handlers[0].Enabled {
		t.Fatal("handler should be disabled")
	}
	if m.Engine("").Has(Stop) {
		t.Fatal("disabled handler must not run")
	}
	if err := m.Toggle(key, true); err != nil {
		t.Fatal(err)
	}
	if !m.Engine("").Has(Stop) {
		t.Fatal("re-enabled handler should run")
	}
}

func TestSetConfigValidatesAndTrustsOwnEdits(t *testing.T) {
	data, folder := t.TempDir(), t.TempDir()
	m := NewManager(data)
	if err := m.SetConfig(ScopeFolder, folder, `{"hooks": {"PreToolUse": [{"matcher": "(", "hooks": []}]}}`); err == nil {
		t.Fatal("want validation error")
	}
	if err := m.SetConfig(ScopeFolder, folder, stopHook); err != nil {
		t.Fatal(err)
	}
	if !m.Engine(folder).Has(Stop) {
		t.Fatal("a config written through Paladin is trusted")
	}
	if err := m.SetConfig(ScopeGlobal, "", stopHook); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(GlobalPath(data)); err != nil {
		t.Fatal(err)
	}
	if err := m.SetConfig(ScopeFolder, "", stopHook); err == nil {
		t.Fatal("folder scope needs a folder")
	}
}

func TestBrokenFileIsReportedNotFatal(t *testing.T) {
	data := t.TempDir()
	write(t, GlobalPath(data), `{"hooks": nope}`)
	srcs := NewManager(data).List("")
	if len(srcs) != 1 || srcs[0].Error == "" {
		t.Fatalf("sources = %+v", srcs)
	}
}
