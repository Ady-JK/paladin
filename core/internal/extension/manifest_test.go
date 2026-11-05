package extension

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writePlugin creates <data>/config/extensions/<name>/ with the given
// files, returning the plugin directory.
func writePlugin(t *testing.T, dataDir, name string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(dataDir, "config", "extensions", name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for file, content := range files {
		path := filepath.Join(dir, file)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func enable(t *testing.T, dataDir string, names ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dataDir, "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	list := make([]string, len(names))
	for i, n := range names {
		list[i] = `"` + n + `"`
	}
	body := `{"enabled":[` + strings.Join(list, ",") + `]}`
	if err := os.WriteFile(filepath.Join(dataDir, "config", "extensions.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestManifestValidation(t *testing.T) {
	cases := []struct {
		name    string
		dir     string
		files   map[string]string
		wantErr string
	}{
		{name: "default entry", dir: "good", files: map[string]string{"index.ts": "export default {tools: []};"}},
		{name: "index.js fallback", dir: "js", files: map[string]string{"index.js": "export default {tools: []};"}},
		{name: "manifest entry", dir: "custom",
			files: map[string]string{"paladin-plugin.json": `{"entry":"main.mjs"}`, "main.mjs": "export default {tools: []};"}},
		{name: "bad dir name", dir: "bad.name", files: map[string]string{"index.ts": ""}, wantErr: "invalid plugin directory name"},
		{name: "no entry", dir: "empty", files: map[string]string{"readme.md": "hi"}, wantErr: "no index.ts or index.js"},
		{name: "entry escapes", dir: "escape",
			files:   map[string]string{"paladin-plugin.json": `{"entry":"../elsewhere.ts"}`},
			wantErr: "must stay inside the plugin folder"},
		{name: "entry absolute",
			dir:     "absolute",
			files:   map[string]string{"paladin-plugin.json": `{"entry":"/etc/passwd"}`},
			wantErr: "must be a relative path"},
		{name: "entry extension",
			dir:     "txt",
			files:   map[string]string{"paladin-plugin.json": `{"entry":"main.txt"}`, "main.txt": ""},
			wantErr: "entry must end in"},
		{name: "unknown field",
			dir:     "typo",
			files:   map[string]string{"paladin-plugin.json": `{"entries":"index.ts"}`, "index.ts": ""},
			wantErr: "unknown field"},
		{name: "bad permission",
			dir:     "perm",
			files:   map[string]string{"paladin-plugin.json": `{"permissions":["net"]}`, "index.ts": ""},
			wantErr: `unknown permission "net"`},
		{name: "env path",
			dir:     "pathenv",
			files:   map[string]string{"paladin-plugin.json": `{"env":{"PATH":"/tmp"}}`, "index.ts": ""},
			wantErr: "may not set PATH"},
		{name: "relative node",
			dir:     "relnode",
			files:   map[string]string{"paladin-plugin.json": `{"node":"node"}`, "index.ts": ""},
			wantErr: "node must be an absolute path"},
		{name: "trailing data",
			dir:     "trailing",
			files:   map[string]string{"paladin-plugin.json": `{"entry":"index.ts"}{"entry":"x.ts"}`, "index.ts": ""},
			wantErr: "trailing data"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dataDir := t.TempDir()
			writePlugin(t, dataDir, tc.dir, tc.files)
			root := filepath.Join(dataDir, "config", "extensions")
			_, err := loadManifest(root, tc.dir)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %v should mention %q", err, tc.wantErr)
			}
		})
	}
}

// A plugin directory that is a symlink out of the extensions folder is
// refused: it would let an enabled name run code from anywhere.
func TestManifestRejectsSymlinkEscape(t *testing.T) {
	dataDir := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "index.ts"), []byte("export default {tools: []};"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dataDir, "config", "extensions")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Skip("symlinks are not available here")
	}
	if _, err := loadManifest(root, "linked"); err == nil || !strings.Contains(err.Error(), "outside the extensions folder") {
		t.Fatalf("symlinked plugin accepted: %v", err)
	}
}

func TestManifestClampsAndDefaults(t *testing.T) {
	low, high, zero := -5, 10_000_000, 0
	m := Manifest{TimeoutMs: 1, IdleMs: &low}
	if m.timeout() != minTimeout || m.idle() != 0 {
		t.Fatalf("clamp low: %v %v", m.timeout(), m.idle())
	}
	m = Manifest{TimeoutMs: 10_000_000, IdleMs: &high}
	if m.timeout() != maxTimeout || m.idle() != maxIdle {
		t.Fatalf("clamp high: %v %v", m.timeout(), m.idle())
	}
	// An explicit 0 idle means the runner is never reaped.
	if (Manifest{IdleMs: &zero}).idle() != 0 {
		t.Fatal("explicit 0 should never reap")
	}
	if (Manifest{}).timeout() != defaultTimeout || (Manifest{}).idle() != defaultIdle {
		t.Fatal("defaults")
	}
}

// entries inside the plugin folder are fine, even through a symlink that
// stays inside.
func TestManifestAcceptsInsideSymlink(t *testing.T) {
	dataDir := t.TempDir()
	dir := writePlugin(t, dataDir, "inside", map[string]string{
		"paladin-plugin.json": `{"entry":"link.ts"}`,
		"real.ts":             "export default {tools: []};",
	})
	if err := os.Symlink(filepath.Join(dir, "real.ts"), filepath.Join(dir, "link.ts")); err != nil {
		t.Skip("symlinks are not available here")
	}
	pl, err := loadManifest(filepath.Join(dataDir, "config", "extensions"), "inside")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(pl.entry, "real.ts") {
		t.Fatalf("entry %s", pl.entry)
	}
}

func TestManifestTooLarge(t *testing.T) {
	dataDir := t.TempDir()
	big := make([]byte, maxManifest+1)
	for i := range big {
		big[i] = ' '
	}
	writePlugin(t, dataDir, "big", map[string]string{"paladin-plugin.json": string(big), "index.ts": ""})
	if _, err := loadManifest(filepath.Join(dataDir, "config", "extensions"), "big"); err == nil ||
		!strings.Contains(err.Error(), "too large") {
		t.Fatalf("oversized manifest accepted: %v", err)
	}
}

// A static manifest tool list needs no process to load.
func TestStaticManifestSkipsList(t *testing.T) {
	if _, err := nodeFor(t); err != nil {
		t.Skip(err)
	}
	dataDir := t.TempDir()
	writePlugin(t, dataDir, "static", map[string]string{
		"paladin-plugin.json": `{"tools":[{"name":"ping","description":"Ping","parameters":{"type":"object"}}]}`,
		"index.ts":            "export default { tools: [] };",
	})
	enable(t, dataDir, "static")
	reg := NewRegistry(dataDir)
	t.Cleanup(reg.Close)
	reg.Start()
	added, failures := reg.Load(t.Context())
	if len(failures) != 0 || len(added) != 1 || added[0].Name() != "extension__static__ping" {
		t.Fatalf("tools %v failures %v", added, failures)
	}
	if len(reg.Runners()) != 0 {
		t.Fatal("listing a static manifest started a runner")
	}
	infos := reg.PluginList()
	if len(infos) != 1 || infos[0].Kind != "plugin" || infos[0].Tools[0] != "extension__static__ping" {
		t.Fatalf("infos %+v", infos)
	}
}

// Cache: an unchanged plugin is not re-listed (no runner spawn), and
// touching the manifest re-reads it.
func TestDeclarationCache(t *testing.T) {
	if _, err := nodeFor(t); err != nil {
		t.Skip(err)
	}
	dataDir := t.TempDir()
	dir := writePlugin(t, dataDir, "cache", map[string]string{
		"index.ts": "export default { tools: [{name: 'a', description: 'A', parameters: {type: 'object'}, execute: () => 'x'}] };",
	})
	enable(t, dataDir, "cache")
	reg := NewRegistry(dataDir)
	t.Cleanup(reg.Close)
	reg.Start()
	if _, failures := reg.Load(t.Context()); len(failures) != 0 {
		t.Fatal(failures)
	}
	spawns := reg.pool.spawns
	if _, failures := reg.Load(t.Context()); len(failures) != 0 {
		t.Fatal(failures)
	}
	if reg.pool.spawns != spawns {
		t.Fatalf("a cached load started %d runner(s)", reg.pool.spawns-spawns)
	}
	// A changed manifest re-lists, so a new static entry is picked up.
	if err := os.WriteFile(filepath.Join(dir, ManifestName),
		[]byte(`{"tools":[{"name":"b","description":"B","parameters":{"type":"object"}}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(filepath.Join(dir, ManifestName), stamp, stamp); err != nil {
		t.Fatal(err)
	}
	added, failures := reg.Load(t.Context())
	if len(failures) != 0 || len(added) != 1 || !strings.HasSuffix(added[0].Name(), "__b") {
		t.Fatalf("after touch: %v %v", added, failures)
	}
}

// Reload drops the warm runner so the next call starts a fresh process.
func TestReloadDropsRunners(t *testing.T) {
	if _, err := nodeFor(t); err != nil {
		t.Skip(err)
	}
	dataDir := t.TempDir()
	writePlugin(t, dataDir, "warm", map[string]string{
		"index.ts": "export default { tools: [{name: 'echo', description: 'Echo', parameters: {type: 'object'}, " +
			"execute: (a) => a}] };",
	})
	enable(t, dataDir, "warm")
	reg := NewRegistry(dataDir)
	t.Cleanup(reg.Close)
	reg.Start()
	added, failures := reg.Load(t.Context())
	if len(failures) != 0 || len(added) != 1 {
		t.Fatalf("%v %v", added, failures)
	}
	if _, err := callTool(t, added[0], map[string]any{"n": 1}); err != nil {
		t.Fatal(err)
	}
	if len(reg.Runners()) != 1 {
		t.Fatalf("expected one warm runner: %+v", reg.Runners())
	}
	reg.Reload()
	if len(reg.Runners()) != 0 {
		t.Fatalf("reload kept runners: %+v", reg.Runners())
	}
	if _, failures := reg.Load(t.Context()); len(failures) != 0 {
		t.Fatal(failures)
	}
}
