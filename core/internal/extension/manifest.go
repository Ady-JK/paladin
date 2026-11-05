package extension

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Plugin directories live under <data>/config/extensions/<name>/ and
// describe themselves in paladin-plugin.json. Loose files stay supported
// through the old spawn-per-call path (see Load).

const (
	// ManifestName is the plugin file inside a plugin directory.
	ManifestName = "paladin-plugin.json"
	// maxManifest caps the manifest, like the extensions.json cap.
	maxManifest = 64 << 10

	defaultTimeout = 120 * time.Second
	minTimeout     = 1 * time.Second
	maxTimeout     = 300 * time.Second
	defaultIdle    = 5 * time.Minute
	maxIdle        = time.Hour
)

// pluginName is a directory name: letters, digits, dash, underscore.
var pluginName = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]{0,63}$`)

// entryExt lists the module extensions a plugin entry may have.
var entryExt = map[string]bool{".ts": true, ".mts": true, ".js": true, ".mjs": true, ".cjs": true}

// Permission values a manifest may declare. They are advisory: Paladin
// shows them, it does not enforce them.
const (
	PermNetwork       = "network"
	PermSpawn         = "spawn"
	PermFSOutsideWork = "fs-outside-workspace"
)

var knownPerms = map[string]bool{PermNetwork: true, PermSpawn: true, PermFSOutsideWork: true}

// Manifest is paladin-plugin.json, decoded strictly. Zero values mean
// "take the default".
type Manifest struct {
	// Entry is the module inside the plugin directory (default index.ts,
	// falling back to index.js).
	Entry string `json:"entry,omitempty"`
	// Tools declares the plugin's tools, so loading needs no subprocess.
	Tools json.RawMessage `json:"tools,omitempty"`
	// TimeoutMs bounds one tool call (default 120000, 1000..300000).
	TimeoutMs int `json:"timeoutMs,omitempty"`
	// IdleMs reaps the warm runner after that long unused (default
	// 300000; an explicit 0 keeps it while the app runs).
	IdleMs *int `json:"idleMs,omitempty"`
	// Env is extra environment for the runner; PATH is refused.
	Env map[string]string `json:"env,omitempty"`
	// Node overrides the Node binary for this plugin (absolute path).
	Node string `json:"node,omitempty"`
	// Permissions are the capabilities the plugin says it needs.
	Permissions []string `json:"permissions,omitempty"`
	// Views are side-pane panels the plugin ships.
	Views []View `json:"views,omitempty"`
	// Cards are inline renderers for the plugin's tool results.
	Cards []Card `json:"cards,omitempty"`
}

// timeout is the per-call timeout the manifest asks for.
func (m Manifest) timeout() time.Duration {
	if m.TimeoutMs <= 0 {
		return defaultTimeout
	}
	return time.Duration(clamp(m.TimeoutMs, int(minTimeout/time.Millisecond), int(maxTimeout/time.Millisecond))) * time.Millisecond
}

// idle is how long a warm runner may sit unused before reaping; an
// explicit 0 means never while the app runs.
func (m Manifest) idle() time.Duration {
	if m.IdleMs == nil {
		return defaultIdle
	}
	return time.Duration(clamp(*m.IdleMs, 0, int(maxIdle/time.Millisecond))) * time.Millisecond
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// plugin is one enabled plugin directory, validated and resolved.
type plugin struct {
	// name is the directory name; exposed tool names use it.
	name string
	// dir is the plugin directory with symlinks resolved.
	dir string
	// entry is the absolute module path.
	entry string
	// Manifest is the plugin's paladin-plugin.json (zero when absent).
	Manifest
	// views and cards are the UI entries that passed validation.
	views []View
	cards []Card
	// uiErrs are UI entries that were skipped; they never fail the
	// plugin's tools.
	uiErrs []error
}

// loadManifest reads and validates one plugin directory. fail reports a
// problem and never falls back to a guess: an explicitly configured
// plugin that is broken stays broken until the user fixes it.
func loadManifest(root, name string) (plugin, error) {
	if !pluginName.MatchString(name) {
		return plugin{}, fmt.Errorf("invalid plugin directory name %q", name)
	}
	dir := filepath.Join(root, name)
	if _, err := os.Lstat(dir); err != nil {
		return plugin{}, fmt.Errorf("plugin %s: %w", name, err)
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return plugin{}, fmt.Errorf("plugin %s: %w", name, err)
	}
	info, err := os.Stat(real)
	if err != nil || !info.IsDir() {
		return plugin{}, fmt.Errorf("plugin %s must be a directory", name)
	}
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		return plugin{}, fmt.Errorf("plugin %s: %w", name, err)
	}
	if !contained(rootReal, real) {
		return plugin{}, fmt.Errorf("plugin %s links outside the extensions folder", name)
	}
	p := plugin{name: name, dir: real}
	if err := p.readManifest(); err != nil {
		return plugin{}, err
	}
	if p.Node != "" {
		if !filepath.IsAbs(p.Node) {
			return plugin{}, fmt.Errorf("plugin %s: node must be an absolute path, got %q", name, p.Node)
		}
		p.Node = filepath.Clean(p.Node)
	}
	if err := validateEnv(p.Env); err != nil {
		return plugin{}, fmt.Errorf("plugin %s: %w", name, err)
	}
	for _, perm := range p.Permissions {
		if !knownPerms[perm] {
			return plugin{}, fmt.Errorf("plugin %s: unknown permission %q", name, perm)
		}
	}
	entry, err := p.resolveEntry()
	if err != nil {
		return plugin{}, err
	}
	p.entry = entry
	p.validateUI()
	return p, nil
}

// readManifest decodes paladin-plugin.json when present.
func (p *plugin) readManifest() error {
	path := filepath.Join(p.dir, ManifestName)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("plugin %s: %w", p.name, err)
	}
	if len(data) > maxManifest {
		return fmt.Errorf("plugin %s: %s is too large", p.name, ManifestName)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p.Manifest); err != nil {
		return fmt.Errorf("plugin %s: %s: %w", p.name, ManifestName, err)
	}
	if dec.More() {
		return fmt.Errorf("plugin %s: %s has trailing data", p.name, ManifestName)
	}
	if p.Tools != nil && !isNull(p.Tools) {
		var decls []declaration
		if err := json.Unmarshal(p.Tools, &decls); err != nil {
			return fmt.Errorf("plugin %s: tools: %w", p.name, err)
		}
	}
	return nil
}

func isNull(raw json.RawMessage) bool {
	t := strings.TrimSpace(string(raw))
	return t == "" || t == "null"
}

// resolveEntry picks the module to import: the manifest entry, else
// index.ts, else index.js. It must stay inside the plugin directory.
func (p *plugin) resolveEntry() (string, error) {
	if p.Entry != "" {
		if filepath.IsAbs(p.Entry) || strings.ContainsAny(p.Entry, `\`) {
			return "", fmt.Errorf("plugin %s: entry must be a relative path, got %q", p.name, p.Entry)
		}
		clean := filepath.Clean(p.Entry)
		if clean == "." || strings.HasPrefix(clean, "..") {
			return "", fmt.Errorf("plugin %s: entry must stay inside the plugin folder, got %q", p.name, p.Entry)
		}
		if !entryExt[filepath.Ext(clean)] {
			return "", fmt.Errorf("plugin %s: entry must end in .ts, .mts, .js, .mjs or .cjs, got %q", p.name, p.Entry)
		}
		path := filepath.Join(p.dir, clean)
		real, err := p.checkFile(path)
		if err != nil {
			return "", err
		}
		return real, nil
	}
	for _, candidate := range []string{"index.ts", "index.js"} {
		path := filepath.Join(p.dir, candidate)
		if _, err := os.Lstat(path); err != nil {
			continue
		}
		real, err := p.checkFile(path)
		if err != nil {
			return "", err
		}
		return real, nil
	}
	return "", fmt.Errorf("plugin %s: no index.ts or index.js (add a paladin-plugin.json entry to pick another file)", p.name)
}

// checkFile requires a regular file inside the plugin directory, and
// returns its resolved path. A symlink is followed only while it stays
// inside.
func (p *plugin) checkFile(path string) (string, error) {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("plugin %s: entry %s: %w", p.name, filepath.Base(path), err)
	}
	if !contained(p.dir, real) {
		return "", fmt.Errorf("plugin %s: entry %s links outside the plugin folder", p.name, filepath.Base(path))
	}
	info, err := os.Stat(real)
	if err != nil {
		return "", fmt.Errorf("plugin %s: entry %s: %w", p.name, filepath.Base(path), err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("plugin %s: entry %s must be a regular file", p.name, filepath.Base(path))
	}
	return real, nil
}

// validateEnv refuses PATH and malformed names: the runner inherits the
// user's PATH and only then takes the manifest's additions.
func validateEnv(env map[string]string) error {
	for k, v := range env {
		if strings.EqualFold(k, "PATH") {
			return errors.New("env may not set PATH")
		}
		if k == "" || strings.ContainsAny(k, "=\x00") || strings.ContainsRune(v, 0) {
			return fmt.Errorf("invalid env entry %q", k)
		}
	}
	return nil
}

// contained reports whether path is base or inside it.
func contained(base, path string) bool {
	if path == base {
		return true
	}
	return strings.HasPrefix(path, strings.TrimSuffix(base, string(filepath.Separator))+string(filepath.Separator))
}
