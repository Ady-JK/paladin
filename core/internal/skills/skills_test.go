package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSkill(t *testing.T, root, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, name, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// userSkills drops the built-in skills, which every listing includes.
func userSkills(all []Skill) []Skill {
	var out []Skill
	for _, s := range all {
		if s.Source != SourceBuiltin {
			out = append(out, s)
		}
	}
	return out
}

func TestListFindsGlobalAndWorkspaceSkills(t *testing.T) {
	dataDir := t.TempDir()
	ws := t.TempDir()
	writeSkill(t, filepath.Join(dataDir, "config", "skills"), "reviewer", "---\ndescription: Reviews code\n---\n# Reviewer\nDo reviews.")
	writeSkill(t, filepath.Join(ws, ".paladin", "skills"), "deployer", "# Deployer\nShips it.")

	got := userSkills(List(dataDir, ws))
	if len(got) != 2 {
		t.Fatalf("got %d skills: %+v", len(got), got)
	}
	names := []string{got[0].Name, got[1].Name}
	if names[0] != "deployer" || names[1] != "reviewer" {
		t.Fatalf("not sorted by name: %v", names)
	}
	if got[1].Description != "Reviews code" {
		t.Fatalf("frontmatter description not read: %q", got[1].Description)
	}
	if got[0].Description != "Deployer" {
		t.Fatalf("body description not read: %q", got[0].Description)
	}
	if got[1].Source != SourceGlobal || got[0].Source != SourceWorkspace {
		t.Fatalf("wrong sources: %+v", got)
	}
	// List rows carry no content.
	if got[0].Content != "" || got[1].Content != "" {
		t.Fatal("list content should be empty")
	}
}

func TestWorkspaceShadowsGlobal(t *testing.T) {
	dataDir := t.TempDir()
	ws := t.TempDir()
	writeSkill(t, filepath.Join(dataDir, "config", "skills"), "helper", "global body")
	writeSkill(t, filepath.Join(ws, ".paladin", "skills"), "HELPER", "workspace body")

	got := userSkills(List(dataDir, ws))
	if len(got) != 1 {
		t.Fatalf("want 1 skill, got %+v", got)
	}
	if got[0].Source != SourceWorkspace {
		t.Fatalf("workspace should win: %+v", got[0])
	}
	loaded, err := Load(dataDir, ws, "helper")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Content != "workspace body" {
		t.Fatalf("workspace load should win: %q", loaded.Content)
	}
}

func TestListSkipsNonSkills(t *testing.T) {
	dataDir := t.TempDir()
	root := filepath.Join(dataDir, "config", "skills")
	// Not a directory.
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Directory without a skill file.
	if err := os.MkdirAll(filepath.Join(root, "empty", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Hidden directory.
	writeSkill(t, root, ".hidden", "nope")
	// Lowercase file name still counts (case-insensitive).
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, ".paladin", "skills", "lower"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, ".paladin", "skills", "lower", "skill.md"), []byte("low"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := userSkills(List(dataDir, ws))
	if len(got) != 1 || got[0].Name != "lower" {
		t.Fatalf("got %+v", got)
	}
}

func TestListMissingRoots(t *testing.T) {
	if got := userSkills(List(t.TempDir(), "")); len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
}

func TestLoad(t *testing.T) {
	dataDir := t.TempDir()
	ws := t.TempDir()
	writeSkill(t, filepath.Join(dataDir, "config", "skills"), "reviewer", "do a review")
	loaded, err := Load(dataDir, ws, "REVIEWER")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Content != "do a review" || loaded.Source != SourceGlobal {
		t.Fatalf("got %+v", loaded)
	}
}

func TestLoadMissingAndInvalid(t *testing.T) {
	dataDir := t.TempDir()
	if _, err := Load(dataDir, "", "nope"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("want not-found, got %v", err)
	}
	for _, bad := range []string{"", "a/b", "..", "a\\b"} {
		if _, err := Load(dataDir, "", bad); err == nil {
			t.Fatalf("want error for %q", bad)
		}
	}
}

func TestSkillSizeCap(t *testing.T) {
	dataDir := t.TempDir()
	root := filepath.Join(dataDir, "config", "skills")
	writeSkill(t, root, "big", strings.Repeat("x", MaxContentBytes+10))
	if _, err := Load(dataDir, "", "big"); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("want size error, got %v", err)
	}
}

func TestInject(t *testing.T) {
	out := Inject("fix the bug", Skill{Name: "debugger", Content: "debug steps"})
	if !strings.Contains(out, "fix the bug") || !strings.Contains(out, "Use this skill (debugger):") || !strings.Contains(out, "debug steps") {
		t.Fatalf("got %q", out)
	}
	out = Inject("  ", Skill{Name: "x", Content: "c"})
	if strings.HasPrefix(out, "\n") {
		t.Fatalf("blank message should not lead with newlines: %q", out)
	}
}

func TestListSkipsOversizeSkills(t *testing.T) {
	dataDir := t.TempDir()
	writeSkill(t, filepath.Join(dataDir, "config", "skills"), "huge", strings.Repeat("x", MaxContentBytes+1))
	writeSkill(t, filepath.Join(dataDir, "config", "skills"), "tiny", "# Tiny\nsmall")
	got := userSkills(List(dataDir, ""))
	if len(got) != 1 || got[0].Name != "tiny" {
		t.Fatalf("oversize skill must not be listed: %+v", got)
	}
	if _, err := Load(dataDir, "", "huge"); err == nil {
		t.Fatal("oversize skill must fail Load")
	}
}

func TestBuiltinSkillsShipAndCanBeReplacedOrTurnedOff(t *testing.T) {
	dataDir, ws := t.TempDir(), t.TempDir()
	byName := map[string]Skill{}
	for _, s := range List(dataDir, ws) {
		byName[s.Name] = s
	}
	for _, name := range []string{"debugging", "test-driven-development", "safe-refactoring", "code-review",
		"security-review", "web-research", "diagrams-and-charts", "writing-docs"} {
		s, ok := byName[name]
		if !ok || s.Source != SourceBuiltin || s.Description == "" || strings.HasPrefix(s.Description, "---") {
			t.Fatalf("built-in %s: %+v", name, s)
		}
		loaded, err := Load(dataDir, ws, name)
		if err != nil || !strings.Contains(loaded.Content, "# ") || loaded.Source != SourceBuiltin {
			t.Fatalf("load %s: %v %+v", name, err, loaded.Source)
		}
		if b, ok := Builtin(name); !ok || b.Content != loaded.Content {
			t.Fatalf("Builtin(%s)", name)
		}
	}
	// A global file of the same name replaces the built-in.
	writeSkill(t, GlobalDir(dataDir), "debugging", "---\ndescription: My way\n---\nMine.")
	if s, _ := Load(dataDir, ws, "debugging"); s.Source != SourceGlobal || !strings.Contains(s.Content, "Mine.") {
		t.Fatalf("override: %+v", s)
	}
	// settings.json turns skills off, whatever their source.
	if err := os.WriteFile(filepath.Join(dataDir, "config", "settings.json"),
		[]byte(`{"disabledSkills":["Web-Research","debugging"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, s := range List(dataDir, ws) {
		if s.Name == "web-research" || s.Name == "debugging" {
			t.Fatalf("turned-off skill listed: %s", s.Name)
		}
	}
	if _, err := Load(dataDir, ws, "web-research"); err == nil || !strings.Contains(err.Error(), "turned off") {
		t.Fatalf("turned-off skill loaded: %v", err)
	}
	if !Disabled(dataDir)["web-research"] {
		t.Fatal("Disabled is case-insensitive")
	}
}
