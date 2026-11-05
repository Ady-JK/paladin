package extension

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func uiManifest(body string) string {
	return `{
		"tools":[{"name":"lookup","description":"Look up a thing","parameters":{"type":"object"}}],
		"views":` + body + `
	}`
}

func writeUI(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "ui"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"ui/board.html": "<!doctype html><p>board</p>",
		"ui/card.js":    "window.__paladin_card = { render: () => 'ok' };",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPluginUIListsViewsAndCards(t *testing.T) {
	dataDir := t.TempDir()
	dir := writePlugin(t, dataDir, "board", map[string]string{
		"index.ts":            "export default { tools: [] };",
		"paladin-plugin.json": uiManifest(`[{"id":"board","title":"Ticket Board","entry":"ui/board.html","mount":"side-pane","permissions":["invokeTool","events"]}]`),
	})
	writeUI(t, dir)
	// Add a card to the same manifest.
	if err := os.WriteFile(filepath.Join(dir, ManifestName), []byte(`{
		"tools":[{"name":"lookup","description":"Look up a thing","parameters":{"type":"object"}}],
		"views":[{"id":"board","title":"Ticket Board","entry":"ui/board.html","mount":"side-pane","permissions":["invokeTool","events"]}],
		"cards":[{"tool":"lookup","entry":"ui/card.js","inline":true}]
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	enable(t, dataDir, "board")
	reg := NewRegistry(dataDir)
	ui := reg.UI()
	if len(ui.Errors) != 0 {
		t.Fatalf("errors: %v", ui.Errors)
	}
	if len(ui.Views) != 1 || ui.Views[0].Plugin != "board" || ui.Views[0].View != "board" || ui.Views[0].Title != "Ticket Board" {
		t.Fatalf("views: %+v", ui.Views)
	}
	if ui.Views[0].Destructive {
		t.Fatalf("invokeTool alone must not mark a view destructive: %+v", ui.Views[0])
	}
	if len(ui.Cards) != 1 || ui.Cards[0].Exposed != "extension__board__lookup" || ui.Cards[0].Tool != "lookup" {
		t.Fatalf("cards: %+v", ui.Cards)
	}
}

func TestPluginUIDestructiveFlag(t *testing.T) {
	dataDir := t.TempDir()
	dir := writePlugin(t, dataDir, "danger", map[string]string{
		"index.ts":            "export default { tools: [] };",
		"paladin-plugin.json": uiManifest(`[{"id":"run","title":"Run","entry":"ui/board.html","mount":"side-pane","permissions":["invokeToolDestructive"]}]`),
	})
	writeUI(t, dir)
	enable(t, dataDir, "danger")
	ui := NewRegistry(dataDir).UI()
	if len(ui.Views) != 1 || !ui.Views[0].Destructive {
		t.Fatalf("views: %+v", ui.Views)
	}
}

func TestPluginUIRejectsBadEntries(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
		keep int
	}{
		{"bad id", `[{"id":"Nope!","title":"x","entry":"ui/board.html"}]`, "id must match", 0},
		{"duplicate id", `[{"id":"a","title":"one","entry":"ui/board.html"},{"id":"a","title":"two","entry":"ui/board.html"}]`, "duplicate view id", 1},
		{"short title", `[{"id":"a","title":" ","entry":"ui/board.html"}]`, "title must be 1 to 48", 0},
		{"long title", `[{"id":"a","title":"` + strings.Repeat("x", 49) + `","entry":"ui/board.html"}]`, "title must be 1 to 48", 0},
		{"bad mount", `[{"id":"a","title":"x","entry":"ui/board.html","mount":"dialog"}]`, "mount must be", 0},
		{"bad perm", `[{"id":"a","title":"x","entry":"ui/board.html","permissions":["network"]}]`, `unknown permission "network"`, 0},
		{"absolute", `[{"id":"a","title":"x","entry":"/tmp/x.html"}]`, "relative path", 0},
		{"escape", `[{"id":"a","title":"x","entry":"../x.html"}]`, "inside the plugin folder", 0},
		{"outside ui", `[{"id":"a","title":"x","entry":"board.html"}]`, "under ui/", 0},
		{"wrong ext", `[{"id":"a","title":"x","entry":"ui/board.js"}]`, "must end in .html", 0},
		{"missing", `[{"id":"a","title":"x","entry":"ui/gone.html"}]`, "no such file", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dataDir := t.TempDir()
			dir := writePlugin(t, dataDir, "p", map[string]string{
				"index.ts":            "export default { tools: [] };",
				"paladin-plugin.json": uiManifest(tc.body),
			})
			writeUI(t, dir)
			enable(t, dataDir, "p")
			ui := NewRegistry(dataDir).UI()
			if len(ui.Views) != tc.keep {
				t.Fatalf("views = %+v, want %d", ui.Views, tc.keep)
			}
			if len(ui.Errors) == 0 || !strings.Contains(strings.Join(ui.Errors, "\n"), tc.want) {
				t.Fatalf("errors %v should mention %q", ui.Errors, tc.want)
			}
		})
	}
}

func TestPluginUICardValidation(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
		keep int
	}{
		{"bad tool", `[{"tool":"Nope!","entry":"ui/card.js"}]`, "needs a name", 0},
		{"duplicate tool", `[{"tool":"lookup","entry":"ui/card.js"},{"tool":"lookup","entry":"ui/card.js"}]`, "duplicate card tool", 1},
		{"not inline", `[{"tool":"lookup","entry":"ui/card.js","inline":false}]`, "only inline cards", 0},
		{"wrong ext", `[{"tool":"lookup","entry":"ui/board.html"}]`, "must end in .js", 0},
		{"not declared", `[{"tool":"other","entry":"ui/card.js"}]`, "not in the manifest's static tools", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dataDir := t.TempDir()
			dir := writePlugin(t, dataDir, "p", map[string]string{
				"index.ts": "export default { tools: [] };",
				"paladin-plugin.json": `{
					"tools":[{"name":"lookup","description":"Look up a thing","parameters":{"type":"object"}}],
					"cards":` + tc.body + `
				}`,
			})
			writeUI(t, dir)
			enable(t, dataDir, "p")
			ui := NewRegistry(dataDir).UI()
			if len(ui.Cards) != tc.keep {
				t.Fatalf("cards = %+v, want %d", ui.Cards, tc.keep)
			}
			if len(ui.Errors) == 0 || !strings.Contains(strings.Join(ui.Errors, "\n"), tc.want) {
				t.Fatalf("errors %v should mention %q", ui.Errors, tc.want)
			}
		})
	}
}

// A broken view never hides the plugin's tools.
func TestPluginUIErrorDoesNotHideTools(t *testing.T) {
	dataDir := t.TempDir()
	dir := writePlugin(t, dataDir, "working", map[string]string{
		"index.ts": "export default { tools: [] };",
		"paladin-plugin.json": `{
			"tools":[{"name":"lookup","description":"Look up a thing","parameters":{"type":"object"}}],
			"views":[{"id":"bad","title":"x","entry":"ui/gone.html"}]
		}`,
	})
	writeUI(t, dir)
	enable(t, dataDir, "working")
	reg := NewRegistry(dataDir)
	added, failures := reg.Load(t.Context())
	if len(failures) != 0 || len(added) != 1 || added[0].Name() != "extension__working__lookup" {
		t.Fatalf("tools %v failures %v", added, failures)
	}
	if ui := reg.UI(); len(ui.Errors) == 0 {
		t.Fatal("UI error was not reported")
	}
}

func TestPluginUIOnlyEnabledAndValidPlugins(t *testing.T) {
	dataDir := t.TempDir()
	for _, name := range []string{"on", "off", "broken"} {
		dir := writePlugin(t, dataDir, name, map[string]string{
			"index.ts":            "export default { tools: [] };",
			"paladin-plugin.json": uiManifest(`[{"id":"` + name + `","title":"` + name + `","entry":"ui/board.html"}]`),
		})
		writeUI(t, dir)
	}
	// A file plugin has no UI and must be ignored, not reported.
	if err := os.WriteFile(filepath.Join(dataDir, "config", "extensions", "legacy.ts"), []byte("export default {tools: []}"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Break the third one's manifest.
	broken := filepath.Join(dataDir, "config", "extensions", "broken", ManifestName)
	if err := os.WriteFile(broken, []byte(`{"views":`), 0o600); err != nil {
		t.Fatal(err)
	}
	enable(t, dataDir, "on", "broken", "legacy.ts")
	ui := NewRegistry(dataDir).UI()
	if len(ui.Views) != 1 || ui.Views[0].Plugin != "on" {
		t.Fatalf("views: %+v", ui.Views)
	}
	if len(ui.Errors) != 1 || !strings.Contains(ui.Errors[0], "plugin broken") {
		t.Fatalf("errors: %v", ui.Errors)
	}
}

func TestPluginUIWithoutConfigIsEmpty(t *testing.T) {
	ui := NewRegistry(t.TempDir()).UI()
	if ui.Views == nil || ui.Cards == nil || len(ui.Views) != 0 || len(ui.Cards) != 0 || len(ui.Errors) != 0 {
		t.Fatalf("ui: %+v", ui)
	}
}

func TestPluginUIRejectsOversizeView(t *testing.T) {
	dataDir := t.TempDir()
	dir := writePlugin(t, dataDir, "big", map[string]string{
		"index.ts":            "export default { tools: [] };",
		"paladin-plugin.json": uiManifest(`[{"id":"big","title":"Big","entry":"ui/big.html"}]`),
	})
	if err := os.MkdirAll(filepath.Join(dir, "ui"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ui", "big.html"), make([]byte, maxViewBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	enable(t, dataDir, "big")
	ui := NewRegistry(dataDir).UI()
	if len(ui.Views) != 0 || len(ui.Errors) == 0 || !strings.Contains(ui.Errors[0], "exceeds") {
		t.Fatalf("ui: %+v", ui)
	}
}
