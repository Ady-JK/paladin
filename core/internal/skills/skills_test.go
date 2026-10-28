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

func TestListFindsGlobalAndWorkspaceSkills(t *testing.T) {
	dataDir := t.TempDir()
	ws := t.TempDir()
	writeSkill(t, filepath.Join(dataDir, "config", "skills"), "reviewer", "---\ndescription: Reviews code\n---\n# Reviewer\nDo reviews.")
	writeSkill(t, filepath.Join(ws, ".paladin", "skills"), "deployer", "# Deployer\nShips it.")

	got := List(dataDir, ws)
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

	got := List(dataDir, ws)
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
	got := List(dataDir, ws)
	if len(got) != 1 || got[0].Name != "lower" {
		t.Fatalf("got %+v", got)
	}
}

func TestListMissingRoots(t *testing.T) {
	if got := List(t.TempDir(), ""); len(got) != 0 {
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
	got := List(dataDir, "")
	if len(got) != 1 || got[0].Name != "tiny" {
		t.Fatalf("oversize skill must not be listed: %+v", got)
	}
	if _, err := Load(dataDir, "", "huge"); err == nil {
		t.Fatal("oversize skill must fail Load")
	}
}
