package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/instructions"
	"github.com/Ady-JK/paladin/core/internal/memory"
)

// writeContextFile writes a file and its parents under dir.
func writeContextFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// entryFor finds the row of one kind and scope ("instructions" rows are
// found by their scope).
func entryFor(view ContextView, kind, scope string) (ContextEntry, bool) {
	for _, entry := range view.Sections {
		if entry.Kind == kind && (scope == "" || entry.Scope == scope) {
			return entry, true
		}
	}
	return ContextEntry{}, false
}

// The box must list exactly the instruction files the prompt carries,
// root first, and no others.
func TestContextSourcesListsInstructionChain(t *testing.T) {
	rt, data := testRuntime(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeContextFile(t, filepath.Join(data, "config", "AGENTS.md"), "Be terse.\n")
	writeContextFile(t, filepath.Join(root, "AGENTS.md"), "Root rules.\n")
	writeContextFile(t, filepath.Join(root, "CLAUDE.md"), "Shadowed by AGENTS.md.\n")
	writeContextFile(t, filepath.Join(root, "sub", "AGENTS.md"), "Sub rules.\n")
	cwd := filepath.Join(root, "sub")

	view := rt.ContextSources(cwd)

	var paths []string
	for _, entry := range view.Sections {
		if entry.Kind == "instructions" {
			paths = append(paths, entry.Path)
		}
	}
	want := []string{
		instructions.GlobalPath(data),
		filepath.Join(root, "AGENTS.md"),
		filepath.Join(root, "sub", "AGENTS.md"),
	}
	if strings.Join(paths, "|") != strings.Join(want, "|") {
		t.Fatalf("instruction rows = %v, want %v", paths, want)
	}
	if view.Files != 3 {
		t.Fatalf("files = %d, want 3", view.Files)
	}
	global, _ := entryFor(view, "instructions", "global")
	if global.Scope != "global" || global.Content != "Be terse." || global.Title != "AGENTS.md" {
		t.Fatalf("global row = %+v", global)
	}
	// bytes totals every auto-loaded source, so sum the instruction
	// rows rather than the whole box.
	instructionsBytes := 0
	for _, entry := range view.Sections {
		if entry.Kind == "instructions" {
			instructionsBytes += entry.Bytes
		}
	}
	if instructionsBytes != len("Be terse.")+len("Root rules.")+len("Sub rules.") {
		t.Fatalf("instruction bytes = %d", instructionsBytes)
	}
}

// A folder outside any git repository still reports its own files.
func TestContextSourcesOutsideRepository(t *testing.T) {
	rt, _ := testRuntime(t)
	cwd := t.TempDir()
	writeContextFile(t, filepath.Join(cwd, "AGENTS.override.md"), "Only this.\n")

	view := rt.ContextSources(cwd)
	if view.Files != 1 || view.Sections[0].Path != filepath.Join(cwd, "AGENTS.override.md") {
		t.Fatalf("view = %+v", view)
	}
	if !strings.Contains(view.Sections[0].Detail, "override") {
		t.Fatalf("detail = %q", view.Sections[0].Detail)
	}
}

// Memory rows follow the enabled scopes of the folder, and the total
// reflects them.
func TestContextSourcesIncludesMemory(t *testing.T) {
	rt, _ := testRuntime(t)
	cwd := t.TempDir()
	if _, err := rt.MemorySet("user", "", []string{"Prefers terse answers"}); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.MemorySet("project", cwd, []string{"Uses pnpm"}); err != nil {
		t.Fatal(err)
	}

	view := rt.ContextSources(cwd)
	if _, ok := entryFor(view, "memory", string(memory.TargetUser)); !ok {
		t.Fatalf("no user memory row in %+v", view.Sections)
	}
	project, ok := entryFor(view, "memory", string(memory.TargetProject))
	if !ok || !strings.Contains(project.Content, "Uses pnpm") {
		t.Fatalf("project memory row = %+v", project)
	}
	if project.Bytes == 0 || !strings.Contains(project.Detail, "1 entry") {
		t.Fatalf("row = %+v", project)
	}
}

// A working directory with no implicit context reports an empty box
// rather than nils, so the UI can render "nothing loaded".
func TestContextSourcesEmptyOmitsMemoryRows(t *testing.T) {
	rt, data := testRuntime(t)
	if _, err := rt.MemorySet("global", "", []string{"Remember the build command"}); err != nil {
		t.Fatal(err)
	}
	// The global store has no entries for this folder, and the user and
	// project scopes are switched off in memory.json, so the box is empty.
	cfg := memory.LoadConfigOr(data)
	cfg.Disabled = []string{"global", "user", "project"}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	writeContextFile(t, memory.ConfigPath(data), string(raw))
	if err := rt.ReloadMemoryConfig(); err != nil {
		t.Fatal(err)
	}

	view := rt.ContextSources(t.TempDir())
	if _, ok := entryFor(view, "memory", ""); ok {
		t.Fatalf("disabled memory still listed: %+v", view.Sections)
	}
	if view.Sections == nil {
		t.Fatal("sections must be an empty slice, not nil")
	}
}
