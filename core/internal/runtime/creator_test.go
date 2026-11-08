package runtime

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/config"
	"github.com/Ady-JK/paladin/core/internal/tools"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

func TestCreatorModeGuardsExtensionEditingAndScaffolding(t *testing.T) {
	data := t.TempDir()
	t.Setenv("PALADIN_CREATOR_DIR", filepath.Join(t.TempDir(), "creator"))
	rt := WithKeyStore(data, config.NewConfig(data), config.NewMemoryKeyStore())
	if rt.CreatorMode() {
		t.Fatal("creator mode should start off")
	}
	if _, err := rt.CreatorWorkspace(); err == nil {
		t.Fatal("created a workspace with creator mode off")
	}
	if err := rt.SetCreatorMode(true); err != nil {
		t.Fatal(err)
	}
	root, err := rt.CreatorWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "core", "hello.ts")
	if err := os.WriteFile(file, []byte("export default {};"), 0o600); err != nil {
		t.Fatal(err)
	}
	view, err := IDEReadFile(root, file)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.CreatorSaveIDEFile(root, file, view.Content+"\n", view.Version); err != nil {
		t.Fatal(err)
	}
	if err := rt.SetCreatorMode(false); err != nil {
		t.Fatal(err)
	}
	view, err = IDEReadFile(root, file)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.CreatorSaveIDEFile(root, file, "changed", view.Version); err == nil || !strings.Contains(err.Error(), "Creator mode") {
		t.Fatalf("saved with Creator mode off: %v", err)
	}
	content, err := os.ReadFile(file)
	if err != nil || string(content) != view.Content {
		t.Fatalf("changed disabled file: %v", err)
	}
	restarted := WithKeyStore(data, config.NewConfig(data), config.NewMemoryKeyStore())
	if restarted.CreatorMode() {
		t.Fatal("disabled state did not persist")
	}
}

func TestCreatorSkillAvailabilityAcrossAgentSurfaces(t *testing.T) {
	data := t.TempDir()
	root := filepath.Join(t.TempDir(), "creator")
	t.Setenv("PALADIN_CREATOR_DIR", root)
	rt := WithKeyStore(data, config.NewConfig(data), config.NewMemoryKeyStore())
	rt.memory = nil
	name := "writing-an-extension"
	check := func(cwd string, want bool) {
		t.Helper()
		listed := false
		for _, s := range rt.ListSkills(cwd) {
			listed = listed || s.Name == name
		}
		sections := (runtimeSections{rt}).SystemSections(transcript.Session{Meta: transcript.Meta{Cwd: cwd}})
		prompt := strings.Join(sections, "\n")
		if listed != want || strings.Contains(prompt, name) != want {
			t.Fatalf("listed=%v prompt=%v, want %v", listed, strings.Contains(prompt, name), want)
		}
		_, err := (runtimeSkills{rt}).LoadSkill(cwd, name)
		if (err == nil) != want {
			t.Fatalf("explicit load: %v", err)
		}
		_, err = tools.NewSkillTool(data).Call(json.RawMessage(`{"name":"writing-an-extension"}`), &tools.ToolCtx{Workspace: cwd})
		if (err == nil) != want {
			t.Fatalf("skill tool load: %v", err)
		}
		view := rt.ContextSources(cwd)
		var text string
		for _, section := range view.Sections {
			if section.Kind == "skills" {
				text = section.Content
			}
		}
		if strings.Contains(text, name) != want {
			t.Fatalf("context preview differs from prompt: %s", text)
		}
	}
	check(root, false)
	if err := rt.SetCreatorMode(true); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.CreatorWorkspace(); err != nil {
		t.Fatal(err)
	}
	check(root, true)
	check(t.TempDir(), false)
	if err := rt.SkillEnabledSet(name, false); err != nil {
		t.Fatal(err)
	}
	check(root, false)
	if err := rt.SkillEnabledSet(name, true); err != nil {
		t.Fatal(err)
	}
	check(root, true)
	if err := rt.SetCreatorMode(false); err != nil {
		t.Fatal(err)
	}
	check(root, false)
}

func TestCreatorSkillsRemainInLargeCatalog(t *testing.T) {
	data := t.TempDir()
	root := filepath.Join(t.TempDir(), "creator")
	t.Setenv("PALADIN_CREATOR_DIR", root)
	rt := WithKeyStore(data, config.NewConfig(data), config.NewMemoryKeyStore())
	if err := rt.SetCreatorMode(true); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < skillCatalogMax+5; i++ {
		dir := filepath.Join(data, "config", "skills", fmt.Sprintf("aaa-%02d", i))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# Other skill"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	catalog := tools.SkillCatalog(data, root, skillCatalogMax)
	for _, name := range []string{"writing-an-extension", "writing-a-plugin-ui", "creating-paladin-ui"} {
		if !strings.Contains(catalog, "- "+name+":") {
			t.Fatalf("Creator skill %s was truncated from catalog", name)
		}
	}
}
