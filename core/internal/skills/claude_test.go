package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolateHome points HOME at a temp dir so a test's Claude roots are the
// ones it creates, not the machine's own.
func isolateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

func TestListFindsClaudeUserAndProjectSkills(t *testing.T) {
	home := isolateHome(t)
	dataDir := t.TempDir()
	ws := t.TempDir()
	writeSkill(t, filepath.Join(home, ".claude", "skills"), "pdf", "---\ndescription: Reads PDFs\n---\n# PDF")
	writeSkill(t, filepath.Join(ws, ".claude", "skills"), "review", "---\ndescription: Reviews a diff\n---\n# Review")

	got := map[string]Skill{}
	for _, s := range userSkills(List(dataDir, ws)) {
		got[s.Name] = s
	}
	if s, ok := got["pdf"]; !ok || s.Source != SourceClaudeUser {
		t.Fatalf("user skill: %+v", got)
	}
	if s, ok := got["review"]; !ok || s.Source != SourceClaudeProject {
		t.Fatalf("project skill: %+v", got)
	}
	if desc := got["pdf"].Description; desc != "Reads PDFs" {
		t.Fatalf("description: %q", desc)
	}
}

func TestListFindsAgentSkillsRoots(t *testing.T) {
	home := isolateHome(t)
	dataDir := t.TempDir()
	ws := t.TempDir()
	writeSkill(t, filepath.Join(home, ".agents", "skills"), "shared", "---\ndescription: From the spec root\n---\n# Shared")
	writeSkill(t, filepath.Join(ws, ".agents", "skills"), "local", "---\ndescription: Project spec root\n---\n# Local")

	got := map[string]Skill{}
	for _, s := range userSkills(List(dataDir, ws)) {
		got[s.Name] = s
	}
	if s, ok := got["shared"]; !ok || s.Source != SourceAgentsUser {
		t.Fatalf("user agents root: %+v", got)
	}
	if s, ok := got["local"]; !ok || s.Source != SourceAgentsProject {
		t.Fatalf("project agents root: %+v", got)
	}
}

func TestListNamespacesPluginSkills(t *testing.T) {
	home := isolateHome(t)
	dataDir := t.TempDir()
	ws := t.TempDir()
	plugin := filepath.Join(home, ".claude", "plugins", "marketplaces", "acme", "review-tools")
	writeSkill(t, filepath.Join(plugin, "skills"), "review", "---\ndescription: Plugin review\n---\n# Plugin review")

	found := userSkills(List(dataDir, ws))
	want := PluginSkillPrefix + "review-tools:review"
	for _, s := range found {
		if s.Name == want {
			if s.Source != SourceClaudePlugin {
				t.Fatalf("source: %+v", s)
			}
			return
		}
	}
	t.Fatalf("plugin skill %q not listed: %+v", want, found)
}

func TestPaladinSkillWinsOverClaude(t *testing.T) {
	home := isolateHome(t)
	dataDir := t.TempDir()
	ws := t.TempDir()
	writeSkill(t, filepath.Join(home, ".claude", "skills"), "reviewer", "---\ndescription: Claude copy\n---\n# Claude")
	writeSkill(t, filepath.Join(ws, ".claude", "skills"), "reviewer", "---\ndescription: Claude project copy\n---\n# Claude project")
	writeSkill(t, filepath.Join(dataDir, "config", "skills"), "reviewer", "---\ndescription: Paladin copy\n---\n# Paladin")

	for _, s := range List(dataDir, ws) {
		if s.Name != "reviewer" {
			continue
		}
		if s.Source != SourceGlobal || s.Description != "Paladin copy" {
			t.Fatalf("Paladin's own skill must win: %+v", s)
		}
		return
	}
	t.Fatal("reviewer missing")
}

func TestProjectClaudeSkillWinsOverUserAndPlugin(t *testing.T) {
	home := isolateHome(t)
	dataDir := t.TempDir()
	ws := t.TempDir()
	writeSkill(t, filepath.Join(home, ".claude", "plugins", "m", "p", "skills"), "deploy", "---\ndescription: Plugin\n---\n# P")
	writeSkill(t, filepath.Join(home, ".claude", "skills"), "deploy", "---\ndescription: User\n---\n# U")
	writeSkill(t, filepath.Join(ws, ".claude", "skills"), "deploy", "---\ndescription: Project\n---\n# W")

	for _, s := range List(dataDir, ws) {
		if s.Name != "deploy" {
			continue
		}
		if s.Source != SourceClaudeProject || s.Description != "Project" {
			t.Fatalf("project Claude skill must win: %+v", s)
		}
		return
	}
	t.Fatal("deploy missing")
}

func TestLoadReadsClaudeSkills(t *testing.T) {
	home := isolateHome(t)
	dataDir := t.TempDir()
	ws := t.TempDir()
	writeSkill(t, filepath.Join(home, ".claude", "skills"), "graphify", "---\ndescription: Graphs a codebase\n---\n# /graphify\nRun the pipeline.")
	writeSkill(t, filepath.Join(ws, ".claude", "skills"), "local-skill", "---\ndescription: Local\n---\n# Local body")

	user, err := Load(dataDir, ws, "graphify")
	if err != nil {
		t.Fatal(err)
	}
	if user.Source != SourceClaudeUser || !strings.Contains(user.Content, "Run the pipeline.") {
		t.Fatalf("%+v", user)
	}
	project, err := Load(dataDir, ws, "local-skill")
	if err != nil {
		t.Fatal(err)
	}
	if project.Source != SourceClaudeProject || !strings.Contains(project.Content, "Local body") {
		t.Fatalf("%+v", project)
	}
}

func TestLoadReadsPluginSkillByName(t *testing.T) {
	home := isolateHome(t)
	dataDir := t.TempDir()
	ws := t.TempDir()
	writeSkill(t, filepath.Join(home, ".claude", "plugins", "m", "review-tools", "skills"), "review",
		"---\ndescription: Plugin review\n---\n# Plugin review body")

	name := PluginSkillPrefix + "review-tools:review"
	s, err := Load(dataDir, ws, name)
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != name || !strings.Contains(s.Content, "Plugin review body") {
		t.Fatalf("%+v", s)
	}
}

func TestValidNameAcceptsPluginForm(t *testing.T) {
	for _, name := range []string{"graphify", "Plugin:x:y", "plugin:a:b"} {
		if !ValidName(name) {
			t.Errorf("%q should be valid", name)
		}
	}
	for _, name := range []string{"", ".", "..", "a/b", `a\b`, "plugin:", "plugin:a", "plugin:a:", "x:y"} {
		if ValidName(name) {
			t.Errorf("%q should be invalid", name)
		}
	}
}

func TestTurnedOffClaudeSkillIsHidden(t *testing.T) {
	home := isolateHome(t)
	dataDir := t.TempDir()
	ws := t.TempDir()
	writeSkill(t, filepath.Join(home, ".claude", "skills"), "noisy", "---\ndescription: Noisy\n---\n# Noisy")
	cfg := `{"disabledSkills":["noisy"]}`
	if err := os.MkdirAll(filepath.Join(dataDir, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "config", "settings.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, s := range List(dataDir, ws) {
		if strings.EqualFold(s.Name, "noisy") {
			t.Fatalf("turned-off skill still listed: %+v", s)
		}
	}
	if _, err := Load(dataDir, ws, "noisy"); err == nil {
		t.Fatal("turned-off skill must not load")
	}
}

func TestOversizeClaudeSkillIsSkipped(t *testing.T) {
	home := isolateHome(t)
	dataDir := t.TempDir()
	ws := t.TempDir()
	writeSkill(t, filepath.Join(home, ".claude", "skills"), "huge", strings.Repeat("x", MaxContentBytes+1))
	for _, s := range List(dataDir, ws) {
		if s.Name == "huge" {
			t.Fatalf("oversize skill listed: %+v", s)
		}
	}
	if _, err := Load(dataDir, ws, "huge"); err == nil {
		t.Fatal("oversize skill must not load")
	}
}
