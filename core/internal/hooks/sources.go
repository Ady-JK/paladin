package hooks

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	ScopeGlobal = "global"
	ScopeFolder = "folder"
)

// GlobalPath is the user's own hooks file; it is trusted implicitly.
func GlobalPath(dataDir string) string { return filepath.Join(dataDir, "config", "hooks.json") }

// FolderPath is the folder's Paladin hooks file.
func FolderPath(folder string) string { return filepath.Join(folder, ".paladin", "hooks.json") }

func statePath(dataDir string) string { return filepath.Join(dataDir, "config", "hooks-state.json") }

// folderFiles are read from a session's folder, in run order. Codex and
// Claude files are read too so existing setups work unchanged.
func folderFiles(folder string) []struct{ path, format string } {
	return []struct{ path, format string }{
		{FolderPath(folder), "paladin"},
		{filepath.Join(folder, ".codex", "hooks.json"), "codex"},
		{filepath.Join(folder, ".claude", "settings.json"), "claude"},
	}
}

// Source is one hooks file as the settings UI shows it.
type Source struct {
	Path     string        `json:"path"`
	Scope    string        `json:"scope"`
	Format   string        `json:"format"`
	Exists   bool          `json:"exists"`
	Editable bool          `json:"editable"`
	Trusted  bool          `json:"trusted"`
	Error    string        `json:"error,omitempty"`
	Warnings []string      `json:"warnings,omitempty"`
	Text     string        `json:"text,omitempty"`
	Handlers []HandlerInfo `json:"handlers"`

	config Config
}

// HandlerInfo is one configured command with its on/off state.
type HandlerInfo struct {
	Key           string `json:"key"`
	Event         Event  `json:"event"`
	Matcher       string `json:"matcher,omitempty"`
	Command       string `json:"command"`
	Timeout       int    `json:"timeout,omitempty"`
	StatusMessage string `json:"statusMessage,omitempty"`
	Enabled       bool   `json:"enabled"`
}

// handlerKey is stable across restarts and edits elsewhere in the file.
func handlerKey(path string, ev Event, matcher, command string) string {
	sum := sha256.Sum256([]byte(path + "\x00" + string(ev) + "\x00" + matcher + "\x00" + command))
	return hex.EncodeToString(sum[:8])
}

func fingerprint(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

type state struct {
	Disabled map[string]bool   `json:"disabled"`
	Trusted  map[string]string `json:"trusted"` // path -> content fingerprint
}

// Manager reads hooks files and remembers trust and on/off state. Files
// are re-read on every call, so edits apply from the next turn.
type Manager struct {
	dataDir string
	mu      sync.Mutex
}

func NewManager(dataDir string) *Manager { return &Manager{dataDir: dataDir} }

func (m *Manager) loadState() state {
	var s state
	if data, err := os.ReadFile(statePath(m.dataDir)); err == nil {
		_ = json.Unmarshal(data, &s)
	}
	if s.Disabled == nil {
		s.Disabled = map[string]bool{}
	}
	if s.Trusted == nil {
		s.Trusted = map[string]string{}
	}
	return s
}

func (m *Manager) saveState(s state) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(statePath(m.dataDir), data)
}

func readSource(path, scope, format string, st state) Source {
	src := Source{Path: path, Scope: scope, Format: format, Editable: format == "paladin", Handlers: []HandlerInfo{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			src.Error = err.Error()
		}
		return src
	}
	src.Exists = true
	if src.Editable {
		src.Text = string(data)
	}
	src.Trusted = scope == ScopeGlobal || st.Trusted[path] == fingerprint(data)
	cfg, warn, err := Parse(data)
	if err != nil {
		src.Error = err.Error()
		return src
	}
	src.config, src.Warnings = cfg, warn
	for _, ev := range Events {
		for _, g := range cfg[ev] {
			for _, h := range g.Hooks {
				key := handlerKey(path, ev, g.Matcher, h.Command)
				src.Handlers = append(src.Handlers, HandlerInfo{
					Key: key, Event: ev, Matcher: g.Matcher, Command: platformCommand(h),
					Timeout: h.Timeout, StatusMessage: h.StatusMessage, Enabled: !st.Disabled[key],
				})
			}
		}
	}
	return src
}

func (m *Manager) sources(cwd string, st state) []Source {
	out := []Source{readSource(GlobalPath(m.dataDir), ScopeGlobal, "paladin", st)}
	if cwd = strings.TrimSpace(cwd); cwd != "" {
		for _, f := range folderFiles(filepath.Clean(cwd)) {
			out = append(out, readSource(f.path, ScopeFolder, f.format, st))
		}
	}
	return out
}

// List returns the global file and, when cwd is set, the folder's
// files, whether or not they exist.
func (m *Manager) List(cwd string) []Source {
	m.mu.Lock()
	st := m.loadState()
	m.mu.Unlock()
	return m.sources(cwd, st)
}

// Engine returns the hooks that may run for cwd: trusted files only,
// minus disabled handlers.
func (m *Manager) Engine(cwd string) *Engine {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	st := m.loadState()
	m.mu.Unlock()
	e := &Engine{groups: map[Event][]activeGroup{}}
	for _, src := range m.sources(cwd, st) {
		if !src.Trusted || src.config == nil {
			continue
		}
		for _, ev := range Events {
			for _, g := range src.config[ev] {
				mt, err := newMatcher(g.Matcher)
				if err != nil {
					continue
				}
				var keep []Handler
				for _, h := range g.Hooks {
					if !st.Disabled[handlerKey(src.Path, ev, g.Matcher, h.Command)] {
						keep = append(keep, h)
					}
				}
				if len(keep) > 0 {
					e.groups[ev] = append(e.groups[ev], activeGroup{matcher: mt, handlers: keep})
				}
			}
		}
	}
	return e
}

// SetConfig validates and writes the global or folder Paladin hooks
// file. Blank text deletes it. A folder file written here is trusted:
// the user just authored it.
func (m *Manager) SetConfig(scope, cwd, text string) error {
	var path string
	switch scope {
	case ScopeGlobal:
		path = GlobalPath(m.dataDir)
	case ScopeFolder:
		if strings.TrimSpace(cwd) == "" {
			return fmt.Errorf("folder hooks need a folder")
		}
		path = FolderPath(filepath.Clean(cwd))
	default:
		return fmt.Errorf("unknown scope %q", scope)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.loadState()
	if strings.TrimSpace(text) == "" {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		delete(st.Trusted, path)
		return m.saveState(st)
	}
	if _, _, err := Parse([]byte(text)); err != nil {
		return err
	}
	data := []byte(strings.TrimRight(text, "\n") + "\n")
	if err := writeAtomic(path, data); err != nil {
		return err
	}
	if scope == ScopeFolder {
		st.Trusted[path] = fingerprint(data)
		return m.saveState(st)
	}
	return nil
}

// Trust approves (or revokes) a folder hooks file. Trust is pinned to
// the file's current content; any later edit needs approval again.
func (m *Manager) Trust(cwd, path string, trust bool) error {
	known := false
	if strings.TrimSpace(cwd) != "" {
		for _, f := range folderFiles(filepath.Clean(cwd)) {
			if f.path == path {
				known = true
			}
		}
	}
	if !known {
		return fmt.Errorf("%s is not a hooks file of %s", path, cwd)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.loadState()
	if !trust {
		delete(st.Trusted, path)
		return m.saveState(st)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	st.Trusted[path] = fingerprint(data)
	return m.saveState(st)
}

// Toggle turns one handler on or off everywhere.
func (m *Manager) Toggle(key string, enabled bool) error {
	if strings.TrimSpace(key) == "" {
		return fmt.Errorf("hook key is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.loadState()
	if enabled {
		delete(st.Disabled, key)
	} else {
		st.Disabled[key] = true
	}
	return m.saveState(st)
}

func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".hooks-*.json")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}
