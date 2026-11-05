package runtime

import (
	"os"
	"path/filepath"
	"testing"
)

// PluginViews reads enabled extension manifests without starting a
// plugin process, so the Plugins menu can open before any turn.
func TestPluginViewsReadsManifests(t *testing.T) {
	rt, dir := testRuntime(t)
	plugin := filepath.Join(dir, "config", "extensions", "board")
	if err := os.MkdirAll(filepath.Join(plugin, "ui"), 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(plugin, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("index.ts", "export default { tools: [] };")
	write("ui/board.html", "<!doctype html><p>board</p>")
	write("paladin-plugin.json", `{
		"views":[{"id":"board","title":"Board","entry":"ui/board.html","permissions":["invokeTool"]}]
	}`)
	config := filepath.Join(dir, "config", "extensions.json")
	if err := os.WriteFile(config, []byte(`{"enabled":["board"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ui := rt.PluginViews()
	if len(ui.Errors) != 0 || len(ui.Views) != 1 {
		t.Fatalf("ui = %+v", ui)
	}
	if ui.Views[0].Plugin != "board" || ui.Views[0].Title != "Board" || ui.Views[0].Mount != "side-pane" {
		t.Fatalf("view = %+v", ui.Views[0])
	}
}
