package extension

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCreatorWorkspaceDiscoversCoreAndUIPlugins(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PALADIN_CREATOR_DIR", root)
	for _, kind := range []string{"core", "ui"} {
		dir := filepath.Join(root, kind, "sample")
		if err := os.MkdirAll(filepath.Join(dir, "ui"), 0o700); err != nil {
			t.Fatal(err)
		}
		manifest := `{"entry":"index.js","tools":[{"name":"echo","description":"Echo","parameters":{"type":"object"}}],"views":[{"id":"panel","title":"Panel","entry":"ui/index.html"}]}`
		for name, body := range map[string]string{"paladin-plugin.json": manifest, "index.js": `export default {tools:[]}`, "ui/index.html": "<p>hello</p>"} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	reg := newTestRegistry(t, t.TempDir())
	got, errs := reg.Load(context.Background())
	if len(errs) != 0 || len(got) != 2 {
		t.Fatalf("tools %v errors %v", got, errs)
	}
	ui := reg.UI()
	if len(ui.Errors) != 0 || len(ui.Views) != 2 {
		t.Fatalf("views %+v", ui)
	}
	names := map[string]bool{}
	for _, tool := range got {
		names[tool.Name()] = true
	}
	if !names["extension__creator_core_sample__echo"] || !names["extension__creator_ui_sample__echo"] {
		t.Fatalf("names %v", names)
	}
}

func TestCreatorUIOnlyPluginNeedsNoNodeEntry(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PALADIN_CREATOR_DIR", root)
	dir := filepath.Join(root, "ui", "panel")
	if err := os.MkdirAll(filepath.Join(dir, "ui"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "paladin-plugin.json"), []byte(`{"tools":[],"views":[{"id":"panel","title":"Panel","entry":"ui/index.html","mount":"overlay"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ui", "index.html"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	reg := newTestRegistry(t, t.TempDir())
	tools, errs := reg.Load(context.Background())
	ui := reg.UI()
	if len(errs) != 0 || len(tools) != 0 || len(ui.Views) != 1 || len(ui.Errors) != 0 {
		t.Fatalf("tools %v errors %v UI %+v", tools, errs, ui)
	}
}
