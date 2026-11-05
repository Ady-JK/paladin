package extension

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/tools"
)

// newTestRegistry builds a registry for dataDir and closes it at the end
// of the test.
func newTestRegistry(t *testing.T, dataDir string) *Registry {
	t.Helper()
	reg := NewRegistry(dataDir)
	reg.Start()
	t.Cleanup(reg.Close)
	return reg
}

func TestInitNeverOverwritesUserFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config", "extensions.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"enabled":["mine.ts"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Init(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := Init(dir); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != `{"enabled":["mine.ts"]}` {
		t.Fatalf("manifest changed: %s, %v", data, err)
	}
}

func TestDisabledExtensionDoesNotExecute(t *testing.T) {
	dir := t.TempDir()
	moduleDir, err := Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "executed")
	code := `import {writeFileSync} from 'node:fs'; writeFileSync(` + quoted(marker) + `, 'yes'); export default {tools:[]};`
	if err := os.WriteFile(filepath.Join(moduleDir, "probe.ts"), []byte(code), 0o600); err != nil {
		t.Fatal(err)
	}
	got, failures := newTestRegistry(t, dir).Load(context.Background())
	if len(got) != 0 || len(failures) != 0 {
		t.Fatalf("disabled module loaded: %v %v", got, failures)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("disabled module executed")
	}
}

func TestEnabledTypeScriptTool(t *testing.T) {
	withNode(t)
	dir := t.TempDir()
	if _, err := Init(dir); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(dir, "config", "extensions.json")
	if err := os.WriteFile(manifest, []byte(`{"enabled":["hello.ts"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, failures := newTestRegistry(t, dir).Load(context.Background())
	if len(failures) != 0 {
		t.Fatal(failures)
	}
	if len(got) != 1 || got[0].Name() != "extension__hello__hello" {
		t.Fatalf("bad tools: %v", got)
	}
	output, err := got[0].Call(json.RawMessage(`{"name":"Ada"}`), &tools.ToolCtx{Workspace: dir, Cancel: context.Background()})
	if err != nil || !strings.Contains(output, `"Hello, Ada"`) {
		t.Fatalf("call: %s %v", output, err)
	}
}

func TestManifestRejectsPathEscape(t *testing.T) {
	dir := t.TempDir()
	if _, err := Init(dir); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(dir, "config", "extensions.json")
	if err := os.WriteFile(manifest, []byte(`{"enabled":["../../escape.ts"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, failures := newTestRegistry(t, dir).Load(context.Background())
	if len(got) != 0 || len(failures) != 1 {
		t.Fatalf("path escape accepted: %v %v", got, failures)
	}
}

// A directory plugin gets a warm runner: one process serves many calls,
// and its tools are exposed under the plugin's directory name.
func TestPluginDirectoryRunsWarm(t *testing.T) {
	withNode(t)
	dir := t.TempDir()
	writePlugin(t, dir, "greeter", map[string]string{
		"index.ts": `let calls = 0;
export default {
  onActivate() { calls += 1; },
  tools: [{
    name: "hello",
    description: "Greet someone",
    parameters: { type: "object", properties: { name: { type: "string" } } },
    execute(args) { return { greeting: "Hello, " + args.name, calls }; },
  }],
};`,
	})
	enable(t, dir, "greeter")
	reg := newTestRegistry(t, dir)
	got, failures := reg.Load(context.Background())
	if len(failures) != 0 {
		t.Fatal(failures)
	}
	if len(got) != 1 || got[0].Name() != "extension__greeter__hello" {
		t.Fatalf("tools %v", got)
	}
	first, err := callTool(t, got[0], map[string]any{"name": "Ada"})
	if err != nil {
		t.Fatal(err)
	}
	// onActivate ran once for the warm process, so the counter proves the
	// second call reused it.
	second, err := callTool(t, got[0], map[string]any{"name": "Bob"})
	if err != nil {
		t.Fatal(err)
	}
	if first.(map[string]any)["calls"].(float64) != 1 || second.(map[string]any)["calls"].(float64) != 1 {
		t.Fatalf("the plugin was not warm: %v then %v", first, second)
	}
	runners := reg.Runners()
	if len(runners) != 1 || runners[0].Plugin != "greeter" || runners[0].PID == 0 {
		t.Fatalf("runners %+v", runners)
	}
	info := reg.PluginList()
	if len(info) != 1 || info[0].Kind != "plugin" || info[0].Error != "" {
		t.Fatalf("plugin info %+v", info)
	}
}

// The exposed name keeps the legacy prefix, so moving foo.ts to foo/ does
// not change what the model calls.
func TestLooseFileAndDirectoryNamesStayStable(t *testing.T) {
	withNode(t)
	dir := t.TempDir()
	body := `export default { tools: [{ name: "hello", description: "Greet", parameters: { type: "object" },
  execute: () => "hi" }] };`
	if err := os.MkdirAll(filepath.Join(dir, "config", "extensions"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config", "extensions", "foo.ts"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	enable(t, dir, "foo.ts")
	reg := newTestRegistry(t, dir)
	fileTools, failures := reg.Load(context.Background())
	if len(failures) != 0 || len(fileTools) != 1 || fileTools[0].Name() != "extension__foo__hello" {
		t.Fatalf("file: %v %v", fileTools, failures)
	}
	if infos := reg.PluginList(); len(infos) != 1 || infos[0].Kind != "file" {
		t.Fatalf("info %+v", infos)
	}
	// The loose file still runs out of process, one per call.
	if out, err := callTool(t, fileTools[0], map[string]any{}); err != nil || out != "hi" {
		t.Fatalf("file call: %v %v", out, err)
	}
	if len(reg.Runners()) != 0 {
		t.Fatal("a loose file got a warm runner")
	}

	writePlugin(t, dir, "foo", map[string]string{"index.ts": body})
	if err := os.Remove(filepath.Join(dir, "config", "extensions", "foo.ts")); err != nil {
		t.Fatal(err)
	}
	enable(t, dir, "foo")
	reg.Reload()
	dirTools, failures := reg.Load(context.Background())
	if len(failures) != 0 || len(dirTools) != 1 || dirTools[0].Name() != fileTools[0].Name() {
		t.Fatalf("migrated: %v %v", dirTools, failures)
	}
}
