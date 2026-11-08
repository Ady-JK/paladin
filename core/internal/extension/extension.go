// Package extension loads explicitly enabled local tools from
// config/extensions: plugin directories (each with its own module and
// optional node_modules) run in one warm Node process per plugin, and
// loose single files keep the older spawn-per-call path.
package extension

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Ady-JK/paladin/core/internal/tools"
)

const maxOutput = 1 << 20

var moduleName = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]*\.(ts|mts|js|mjs)$`)
var toolName = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]*$`)

// listTimeout bounds a plugin's tool declarations.
const listTimeout = 10 * time.Second

type configFile struct {
	Enabled []string `json:"enabled"`
}

type declaration struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// PluginInfo is one enabled extension as the tools UI shows it.
type PluginInfo struct {
	Name string `json:"name"`
	// Kind is "plugin" for a directory and "file" for a loose module.
	Kind string `json:"kind"`
	Path string `json:"path"`
	// Permissions are the capabilities the plugin declares; they are
	// advisory (Paladin shows them, it does not enforce them).
	Permissions []string `json:"permissions,omitempty"`
	// Tools are the exposed tool names.
	Tools []string `json:"tools,omitempty"`
	// Error explains why the plugin is not usable, if it is not.
	Error string `json:"error,omitempty"`
}

// Registry loads the enabled extensions and owns their warm runners. One
// registry lives in the runtime for the whole process, so declarations
// and processes are reused across turns; Reload drops both.
type Registry struct {
	dataDir string
	pool    *Pool

	mu    sync.Mutex
	cache map[string]cached
	// plugins remembers the last load for the tools UI.
	plugins []PluginInfo
	// startOnce starts the idle reaper once.
	startOnce sync.Once
	closed    bool
}

// cached is one extension's declarations, valid while its files are
// unchanged.
type cached struct {
	stamp string
	decls []declaration
}

// NewRegistry builds a registry rooted at the data directory.
func NewRegistry(dataDir string) *Registry {
	return &Registry{dataDir: dataDir, pool: NewPool(), cache: map[string]cached{}}
}

// Start begins reaping idle runners.
func (r *Registry) Start() {
	r.startOnce.Do(func() { r.pool.Start() })
}

// Close stops every runner and removes the generated runner script.
// Nothing here may block the app's exit for long.
func (r *Registry) Close() {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	r.mu.Unlock()
	r.pool.Close()
}

// Runners reports the plugins with a live runner.
func (r *Registry) Runners() []RunnerInfo {
	out := r.pool.Running()
	sort.Slice(out, func(i, j int) bool { return out[i].Plugin < out[j].Plugin })
	return out
}

// Reload forgets the cached declarations and stops every runner, so the
// next turn reloads from disk. Calls already in flight finish on the
// runner they started on.
func (r *Registry) Reload() {
	r.mu.Lock()
	r.cache = map[string]cached{}
	r.plugins = nil
	r.mu.Unlock()
	r.pool.Drain()
}

// PluginList reports the enabled extensions of the last load.
func (r *Registry) PluginList() []PluginInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]PluginInfo, len(r.plugins))
	copy(out, r.plugins)
	return out
}

// Load returns the tools of every enabled extension. Declarations are
// cached per process and re-read only when an extension's files change,
// so a steady state spawns no subprocess at all. A broken extension is
// reported and skipped; it never blocks the built-in tools.
func (r *Registry) Load(ctx context.Context) ([]tools.Tool, []error) {
	sources, failures := r.sources()
	var result []tools.Tool
	var infos []PluginInfo
	seen := map[string]bool{}
	seenTools := map[string]bool{}
	for _, source := range sources {
		name := source.exposed
		if seen[name] {
			failures = append(failures, fmt.Errorf("duplicate plugin name %q", name))
			continue
		}
		seen[name] = true
		var added []tools.Tool
		var info PluginInfo
		var err error
		switch {
		case source.legacy:
			added, info, err = r.loadFile(ctx, source.name)
			if err != nil {
				failures = append(failures, err)
			}
		case pluginName.MatchString(source.name):
			added, info, err = r.loadPlugin(ctx, source.root, source.name, name)
			if err != nil {
				failures = append(failures, err)
			}
		default:
			failures = append(failures, fmt.Errorf("invalid extension name %q: use a plugin folder like foo or "+
				"a legacy file like foo.ts", name))
			continue
		}
		infos = append(infos, info)
		for _, tool := range added {
			if seenTools[tool.Name()] {
				failures = append(failures, fmt.Errorf("duplicate extension tool %q", tool.Name()))
				continue
			}
			seenTools[tool.Name()] = true
			result = append(result, tool)
		}
	}
	r.mu.Lock()
	r.plugins = infos
	r.mu.Unlock()
	return result, failures
}

// loadPlugin loads one plugin directory, from cache when nothing changed.
func (r *Registry) loadPlugin(ctx context.Context, root, folder, name string) ([]tools.Tool, PluginInfo, error) {
	pl, err := loadManifest(root, folder)
	if err != nil {
		return nil, PluginInfo{Name: name, Kind: "plugin", Error: err.Error()}, err
	}
	pl.name = name
	info := PluginInfo{Name: name, Kind: "plugin", Path: pl.dir, Permissions: pl.Permissions}
	stamp := pluginStamp(pl)
	decls, cached := r.lookup(name, stamp)
	if !cached {
		decls, err = r.declare(ctx, pl)
		if err != nil {
			info.Error = err.Error()
			return nil, info, fmt.Errorf("plugin %s: %w", name, err)
		}
		if err := validDecls(decls); err != nil {
			info.Error = err.Error()
			return nil, info, fmt.Errorf("plugin %s: %w", name, err)
		}
		r.store(name, stamp, decls)
	}
	var out []tools.Tool
	for _, decl := range decls {
		exposed := "extension__" + name + "__" + decl.Name
		info.Tools = append(info.Tools, exposed)
		out = append(out, &pluginTool{registry: r, plugin: pl, exposed: exposed, tool: decl.Name,
			decl: decl, timeout: pl.timeout()})
	}
	return out, info, nil
}

// declare returns a plugin's tool declarations: the manifest's static
// list when it has one, else a `list` request to the plugin's runner.
func (r *Registry) declare(ctx context.Context, pl plugin) ([]declaration, error) {
	if len(pl.Tools) > 0 && !isNull(pl.Tools) {
		var decls []declaration
		if err := json.Unmarshal(pl.Tools, &decls); err != nil {
			return nil, fmt.Errorf("%s: tools: %w", ManifestName, err)
		}
		return decls, nil
	}
	listCtx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	out, err := r.pool.List(listCtx, pl, listTimeout)
	if err != nil {
		return nil, err
	}
	return decodeDecls(out)
}

// loadFile loads a legacy loose module through the spawn-per-call path.
func (r *Registry) loadFile(ctx context.Context, name string) ([]tools.Tool, PluginInfo, error) {
	path := filepath.Join(r.dataDir, "config", "extensions", name)
	prefix := strings.TrimSuffix(name, filepath.Ext(name))
	info := PluginInfo{Name: prefix, Kind: "file", Path: path}
	stat, err := os.Lstat(path)
	if err != nil || !stat.Mode().IsRegular() {
		err = fmt.Errorf("extension %s must be a regular file", name)
		info.Error = err.Error()
		return nil, info, err
	}
	stamp := fileStamp(stat)
	decls, cached := r.lookup(name, stamp)
	if !cached {
		loadCtx, cancel := context.WithTimeout(ctx, listTimeout)
		out, err := runLegacy(loadCtx, path, "list", "", nil)
		cancel()
		if err != nil {
			info.Error = err.Error()
			return nil, info, fmt.Errorf("extension %s: %w", name, err)
		}
		decls, err = decodeDecls(out)
		if err != nil {
			info.Error = err.Error()
			return nil, info, fmt.Errorf("extension %s: %w", name, err)
		}
		if err := validDecls(decls); err != nil {
			info.Error = err.Error()
			return nil, info, fmt.Errorf("extension %s: %w", name, err)
		}
		r.store(name, stamp, decls)
	}
	if len(decls) > 0 {
		fmt.Fprintf(os.Stderr, "extension %s: loose files are deprecated; move it into the %s folder\n",
			name, prefix)
	}
	var out []tools.Tool
	for _, decl := range decls {
		exposed := "extension__" + prefix + "__" + decl.Name
		info.Tools = append(info.Tools, exposed)
		out = append(out, &legacyTool{path: path, exposed: exposed, tool: decl.Name, decl: decl})
	}
	return out, info, nil
}

func decodeDecls(out []byte) ([]declaration, error) {
	var decls []declaration
	if err := json.Unmarshal(out, &decls); err != nil {
		return nil, err
	}
	return decls, nil
}

// validDecls checks the declarations the model will see.
func validDecls(decls []declaration) error {
	for _, decl := range decls {
		var schema struct {
			Type string `json:"type"`
		}
		if !toolName.MatchString(decl.Name) || strings.TrimSpace(decl.Description) == "" ||
			json.Unmarshal(decl.Parameters, &schema) != nil || schema.Type != "object" {
			return fmt.Errorf("invalid tool %q: it needs a name, a description and an object schema", decl.Name)
		}
	}
	return nil
}

// lookup returns cached declarations when the stamp still matches.
func (r *Registry) lookup(name, stamp string) ([]declaration, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.cache[name]
	if !ok || c.stamp != stamp {
		return nil, false
	}
	return c.decls, true
}

func (r *Registry) store(name, stamp string, decls []declaration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cache[name] = cached{stamp: stamp, decls: decls}
}

// pluginStamp fingerprints the files a plugin's declarations come from:
// a changed manifest, entry or package.json re-reads them.
func pluginStamp(pl plugin) string {
	return strings.Join([]string{
		fileStampPath(pl.dir, ManifestName),
		fileStampPath(pl.dir, "package.json"),
		fileStampPath(filepath.Dir(pl.entry), filepath.Base(pl.entry)),
	}, "|")
}

func fileStampPath(dir, name string) string {
	stat, err := os.Lstat(filepath.Join(dir, name))
	if err != nil {
		return name + ":missing"
	}
	return name + ":" + fileStamp(stat)
}

// fileStamp is the identity of one file: its size, its modification
// time and, where the platform has one, its change time, so a rewrite
// inside the same coarse mtime tick still counts as a change.
func fileStamp(stat os.FileInfo) string {
	return fmt.Sprintf("%d:%d", stat.ModTime().UnixNano(), stat.Size()) + extraStamp(stat)
}

// pluginTool is one tool of a plugin directory, served by its runner.
type pluginTool struct {
	registry *Registry
	plugin   plugin
	exposed  string
	tool     string
	decl     declaration
	timeout  time.Duration
}

func (t *pluginTool) Name() string { return t.exposed }

// Extension marks the tool for the permission gate, which asks per call
// unless a rule allows it.
func (t *pluginTool) Extension() bool { return true }

// ExtensionInfo names the plugin and the plugin's own tool, so rules can
// say "plugin/tool".
func (t *pluginTool) ExtensionInfo() tools.ExtensionInfo {
	return tools.ExtensionInfo{Plugin: t.plugin.name, Tool: t.tool}
}

func (t *pluginTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{Name: t.exposed, Description: t.decl.Description, Parameters: t.decl.Parameters}
}

func (t *pluginTool) Call(args json.RawMessage, tc *tools.ToolCtx) (string, error) {
	raw, err := t.registry.pool.Call(callContext(tc), t.plugin, t.tool, callInput(args, tc), t.timeout)
	if err != nil {
		return "", toolFailure(err)
	}
	var out string
	if json.Unmarshal(raw, &out) == nil {
		return out, nil
	}
	return string(raw), nil
}

// legacyTool is one tool of a loose module, spawned per call.
type legacyTool struct {
	path    string
	exposed string
	tool    string
	decl    declaration
}

func (t *legacyTool) Name() string { return t.exposed }

func (t *legacyTool) Extension() bool { return true }

func (t *legacyTool) ExtensionInfo() tools.ExtensionInfo {
	return tools.ExtensionInfo{Plugin: strings.TrimSuffix(filepath.Base(t.path), filepath.Ext(t.path)), Tool: t.tool}
}

func (t *legacyTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{Name: t.exposed, Description: t.decl.Description, Parameters: t.decl.Parameters}
}

func (t *legacyTool) Call(args json.RawMessage, tc *tools.ToolCtx) (string, error) {
	ctx, cancel := context.WithTimeout(callContext(tc), defaultTimeout)
	defer cancel()
	out, err := runLegacy(ctx, t.path, "call", t.tool, callInput(args, tc))
	if err != nil {
		return "", toolFailure(err)
	}
	return string(out), nil
}

// callContext is the turn's cancellation, or the background.
func callContext(tc *tools.ToolCtx) context.Context {
	if tc != nil && tc.Cancel != nil {
		return tc.Cancel
	}
	return context.Background()
}

// callInput is what a plugin's execute receives: its arguments plus where
// it runs.
func callInput(args json.RawMessage, tc *tools.ToolCtx) json.RawMessage {
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	ctx := map[string]string{}
	if tc != nil {
		ctx = map[string]string{"workspace": tc.Workspace, "project": tc.Project, "sessionId": tc.SessionID}
	}
	data, err := json.Marshal(map[string]any{"args": args, "context": ctx})
	if err != nil {
		return json.RawMessage(`{"args":{}}`)
	}
	return data
}

// toolFailure keeps the failure model-visible and says what happened.
func toolFailure(err error) error {
	return tools.WithContent(`{"error":`+quoted(err.Error())+`}`, err.Error())
}

func quoted(s string) string { data, _ := json.Marshal(s); return string(data) }

type cappedBuffer struct {
	bytes.Buffer
	exceeded bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	remaining := maxOutput - b.Len()
	if len(p) > remaining {
		b.exceeded = true
	}
	if remaining > 0 {
		b.Buffer.Write(p[:min(len(p), remaining)])
	}
	return len(p), nil
}

// runLegacy runs one loose module out of process: the pre-plugin path,
// kept for files still listed in extensions.json.
func runLegacy(ctx context.Context, path, action, name string, input []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "node", "--input-type=module", "-e", legacyRunner, path, action, name)
	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}
	cmd.Dir = filepath.Dir(path)
	var out, stderr cappedBuffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if out.exceeded {
		return nil, fmt.Errorf("extension output exceeds %d bytes", maxOutput)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return out.Bytes(), nil
}

// legacyRunner imports one loose module, prints only protocol JSON to
// stdout, and sends console logging to stderr. Node strips the module's
// erasable TypeScript syntax itself.
const legacyRunner = `
import { pathToFileURL } from 'node:url';
import { readFileSync } from 'node:fs';
console.log = (...args) => console.error(...args);
const mod = (await import(pathToFileURL(process.argv[1]).href)).default;
if (!mod || !Array.isArray(mod.tools)) throw new Error('default export needs a tools array');
if (process.argv[2] === 'list') {
  process.stdout.write(JSON.stringify(mod.tools.map(({name, description, parameters}) => ({name, description, parameters}))));
} else {
  const tool = mod.tools.find(t => t.name === process.argv[3]);
  if (!tool || typeof tool.execute !== 'function') throw new Error('tool not found');
  const input = JSON.parse(readFileSync(0, 'utf8'));
  const value = await tool.execute(input.args, input.context);
  process.stdout.write(typeof value === 'string' ? value : JSON.stringify(value ?? null));
}
`
