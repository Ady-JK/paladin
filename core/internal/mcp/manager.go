package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Ady-JK/paladin/core/internal/tools"
)

// Server scopes.
const (
	ScopeGlobal = "global"
	ScopeFolder = "folder"
)

// Server states.
const (
	StateDisabled  = "disabled"
	StateUntrusted = "untrusted"
	StateStopped   = "stopped"
	StateStarting  = "starting"
	StateReady     = "ready"
	StateFailed    = "failed"
	// StateRemoved is only ever emitted: the server left its config.
	StateRemoved = "removed"
)

// Crash restarts: a server that dies after becoming ready is restarted
// on next use, at most crashBudget times per crashWindow.
const (
	crashBudget = 3
	crashWindow = 5 * time.Minute
)

const (
	maxInstructions = 2000
	maxStatusStderr = 4 << 10
)

// ToolStatus describes one exposed tool.
type ToolStatus struct {
	Name        string `json:"name"`
	Exposed     string `json:"exposed"`
	Description string `json:"description,omitempty"`
	ReadOnly    bool   `json:"readOnly"`
	Destructive bool   `json:"destructive"`
}

// ServerStatus is a snapshot of one configured server.
type ServerStatus struct {
	Name      string          `json:"name"`
	Scope     string          `json:"scope"`
	Folder    string          `json:"folder,omitempty"`
	Transport string          `json:"transport"`
	State     string          `json:"state"`
	Error     string          `json:"error,omitempty"`
	Shadowed  bool            `json:"shadowed,omitempty"`
	Tools     []ToolStatus    `json:"tools"`
	Server    *Implementation `json:"server,omitempty"`
	Stderr    string          `json:"stderr,omitempty"`
	Restarts  int             `json:"restarts"`
}

// ConfigFile is one mcp.json as the settings editor sees it.
type ConfigFile struct {
	Path   string `json:"path"`
	Text   string `json:"text"`
	Exists bool   `json:"exists"`
}

type serverKey struct{ scope, folder, name string }

// Manager owns every configured MCP server: it reads the global and
// per-folder mcp.json files, starts servers on first use, restarts
// crashed ones within a budget and reports state changes to emit.
// All methods are safe for concurrent use.
type Manager struct {
	dataDir string
	emit    func(ServerStatus)
	trust   *trustStore
	ctx     context.Context
	cancel  context.CancelFunc

	mu      sync.Mutex
	closed  bool
	servers map[serverKey]*server
	cfgErrs map[string]string
}

// NewManager reads configs from dataDir (and folders as sessions use
// them); emit, if non-nil, receives every status change.
func NewManager(dataDir string, emit func(ServerStatus)) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		dataDir: dataDir,
		emit:    emit,
		trust:   newTrustStore(filepath.Join(dataDir, "config", "mcp-trust.json")),
		ctx:     ctx,
		cancel:  cancel,
		servers: map[serverKey]*server{},
		cfgErrs: map[string]string{},
	}
}

func cleanFolder(folder string) string {
	if folder == "" {
		return ""
	}
	return filepath.Clean(folder)
}

func (m *Manager) path(scope, folder string) (string, error) {
	switch scope {
	case ScopeGlobal:
		return GlobalPath(m.dataDir), nil
	case ScopeFolder:
		if folder == "" {
			return "", errors.New("folder scope needs a folder")
		}
		return FolderPath(folder), nil
	}
	return "", fmt.Errorf("unknown scope %q", scope)
}

func (m *Manager) publish(st ServerStatus) {
	if m.emit != nil {
		m.emit(st)
	}
}

// StartGlobal starts every enabled global server in the background.
func (m *Manager) StartGlobal() {
	m.sync("")
	for _, s := range m.active("") {
		s.ensure()
	}
}

// Tools returns wrappers for every ready server visible from folder,
// starting servers as needed. It waits for starting servers until they
// settle or ctx ends; failures are reported through status, never
// returned, so one broken server cannot block a turn.
func (m *Manager) Tools(ctx context.Context, folder string) []tools.Tool {
	folder = cleanFolder(folder)
	m.sync(folder)
	active := m.active(folder)
	waits := make([]<-chan struct{}, 0, len(active))
	for _, s := range active {
		if ch := s.ensure(); ch != nil {
			waits = append(waits, ch)
		}
	}
	for _, ch := range waits {
		select {
		case <-ch:
		case <-ctx.Done():
		}
	}
	var out []tools.Tool
	seen := map[string]bool{}
	for _, s := range active {
		for _, t := range s.wrappers() {
			if !seen[t.Name()] {
				seen[t.Name()] = true
				out = append(out, t)
			}
		}
	}
	return out
}

// Instructions joins the usage notes of the ready servers visible from
// folder, for the system prompt.
func (m *Manager) Instructions(folder string) string {
	var parts []string
	for _, s := range m.active(cleanFolder(folder)) {
		s.mu.Lock()
		text := strings.TrimSpace(s.instructions)
		ready := s.state == StateReady
		s.mu.Unlock()
		if ready && text != "" {
			parts = append(parts, fmt.Sprintf("### MCP server %q\n%s", s.key.name, clip(text, maxInstructions)))
		}
	}
	return strings.Join(parts, "\n\n")
}

// Status lists the global servers and folder's own servers.
func (m *Manager) Status(folder string) []ServerStatus {
	folder = cleanFolder(folder)
	m.sync(folder)
	m.mu.Lock()
	var list []*server
	for k, s := range m.servers {
		if k.scope == ScopeGlobal || (folder != "" && k.folder == folder) {
			list = append(list, s)
		}
	}
	m.mu.Unlock()
	local := map[string]bool{}
	for _, s := range list {
		if s.key.scope == ScopeFolder && s.runnable() {
			local[s.key.name] = true
		}
	}
	out := make([]ServerStatus, 0, len(list))
	for _, s := range list {
		st := s.status()
		st.Shadowed = s.key.scope == ScopeGlobal && local[s.key.name]
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Scope != out[j].Scope {
			return out[i].Scope == ScopeGlobal
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// ConfigErrors maps scope to the parse error of its mcp.json, if any.
func (m *Manager) ConfigErrors(folder string) map[string]string {
	folder = cleanFolder(folder)
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]string{}
	if e := m.cfgErrs[GlobalPath(m.dataDir)]; e != "" {
		out[ScopeGlobal] = e
	}
	if folder != "" {
		if e := m.cfgErrs[FolderPath(folder)]; e != "" {
			out[ScopeFolder] = e
		}
	}
	return out
}

// Trusted reports whether folder's current mcp.json is approved. A
// folder without one counts as trusted: there is nothing to run.
func (m *Manager) Trusted(folder string) bool {
	folder = cleanFolder(folder)
	data, err := os.ReadFile(FolderPath(folder))
	if err != nil {
		return true
	}
	return m.trust.Trusted(folder, Fingerprint(data))
}

// Trust approves (or revokes) folder's current mcp.json.
func (m *Manager) Trust(folder string, trust bool) error {
	folder = cleanFolder(folder)
	if folder == "" {
		return errors.New("trust needs a folder")
	}
	fp := ""
	if trust {
		data, err := os.ReadFile(FolderPath(folder))
		if err != nil {
			return fmt.Errorf("read %s: %w", FolderPath(folder), err)
		}
		fp = Fingerprint(data)
	}
	if err := m.trust.Set(folder, fp); err != nil {
		return err
	}
	m.syncScope(ScopeFolder, folder)
	return nil
}

// Config returns one mcp.json for editing.
func (m *Manager) Config(scope, folder string) (ConfigFile, error) {
	path, err := m.path(scope, cleanFolder(folder))
	if err != nil {
		return ConfigFile{}, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return ConfigFile{Path: path, Text: "{\n  \"mcpServers\": {}\n}\n"}, nil
	}
	if err != nil {
		return ConfigFile{}, err
	}
	return ConfigFile{Path: path, Text: string(data), Exists: true}, nil
}

// SetConfig validates and saves an mcp.json, then applies it. A folder
// config written here is trusted: the user just wrote it.
func (m *Manager) SetConfig(scope, folder, text string) error {
	folder = cleanFolder(folder)
	if _, err := ParseFile([]byte(text)); err != nil {
		return err
	}
	return m.write(scope, folder, []byte(text), true)
}

// SetEnabled flips one server's enabled flag in its config file. A
// trusted folder config stays trusted.
func (m *Manager) SetEnabled(scope, folder, name string, enabled bool) error {
	folder = cleanFolder(folder)
	path, err := m.path(scope, folder)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("no MCP server %q in %s", name, path)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	list, _ := doc["mcpServers"].(map[string]any)
	entry, ok := list[name].(map[string]any)
	if !ok {
		return fmt.Errorf("no MCP server %q in %s", name, path)
	}
	if enabled {
		delete(entry, "enabled")
	} else {
		entry["enabled"] = false
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return err
	}
	trusted := scope == ScopeGlobal || m.trust.Trusted(folder, Fingerprint(data))
	return m.write(scope, folder, buf.Bytes(), trusted)
}

func (m *Manager) write(scope, folder string, data []byte, trust bool) error {
	path, err := m.path(scope, folder)
	if err != nil {
		return err
	}
	data = []byte(strings.TrimRight(string(data), "\n") + "\n")
	if err := WriteFile(path, data); err != nil {
		return err
	}
	if scope == ScopeFolder && trust {
		if err := m.trust.Set(folder, Fingerprint(data)); err != nil {
			return err
		}
	}
	m.syncScope(scope, folder)
	return nil
}

// Restart stops a server and starts it again, clearing any failure.
func (m *Manager) Restart(scope, folder, name string) error {
	folder = cleanFolder(folder)
	if scope == ScopeGlobal {
		folder = ""
	}
	m.syncScope(scope, folder)
	m.mu.Lock()
	s := m.servers[serverKey{scope, folder, name}]
	m.mu.Unlock()
	if s == nil {
		return fmt.Errorf("no MCP server %q", name)
	}
	if !s.runnable() {
		return fmt.Errorf("MCP server %q is %s", name, s.status().State)
	}
	s.reset()
	s.ensure()
	return nil
}

// Close stops every server and waits for them to exit.
func (m *Manager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	m.cancel()
	list := make([]*server, 0, len(m.servers))
	for _, s := range m.servers {
		list = append(list, s)
	}
	m.servers = map[serverKey]*server{}
	m.mu.Unlock()
	var wg sync.WaitGroup
	for _, s := range list {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.remove()
		}()
	}
	wg.Wait()
}

func (m *Manager) sync(folder string) {
	m.syncScope(ScopeGlobal, "")
	if folder != "" {
		m.syncScope(ScopeFolder, folder)
	}
}

// syncScope reconciles one config file with the running servers. A
// file that fails to parse leaves the servers as they were, so a
// half-saved edit doesn't take everything down.
func (m *Manager) syncScope(scope, folder string) {
	path, err := m.path(scope, folder)
	if err != nil {
		return
	}
	f, data, err := readFile(path)
	trusted := scope == ScopeGlobal || (data != nil && m.trust.Trusted(folder, Fingerprint(data)))
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	if err != nil {
		m.cfgErrs[path] = err.Error()
		m.mu.Unlock()
		return
	}
	delete(m.cfgErrs, path)
	var stale, fresh []*server
	for name, cfg := range f.McpServers {
		k := serverKey{scope, folder, name}
		sig := signature(cfg)
		old := m.servers[k]
		if old != nil && old.sig == sig && old.trusted == trusted {
			continue
		}
		if old != nil {
			stale = append(stale, old)
		}
		s := newServer(m, k, cfg, sig, trusted)
		m.servers[k] = s
		fresh = append(fresh, s)
	}
	var removed []*server
	for k, s := range m.servers {
		if k.scope == scope && k.folder == folder {
			if _, ok := f.McpServers[k.name]; !ok {
				delete(m.servers, k)
				removed = append(removed, s)
			}
		}
	}
	m.mu.Unlock()
	for _, s := range append(stale, removed...) {
		go s.remove()
	}
	for _, s := range removed {
		st := s.status()
		st.State, st.Tools = StateRemoved, nil
		m.publish(st)
	}
	for _, s := range fresh {
		m.publish(s.status())
	}
}

// active lists the runnable servers folder sees, folder servers
// replacing global ones of the same name.
func (m *Manager) active(folder string) []*server {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	var global, local []*server
	for k, s := range m.servers {
		switch {
		case k.scope == ScopeGlobal:
			global = append(global, s)
		case folder != "" && k.folder == folder:
			local = append(local, s)
		}
	}
	names := map[string]bool{}
	var out []*server
	for _, s := range local {
		if s.runnable() {
			names[s.key.name] = true
			out = append(out, s)
		}
	}
	for _, s := range global {
		if s.runnable() && !names[s.key.name] {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].key.name < out[j].key.name })
	return out
}

func signature(cfg ServerConfig) string {
	data, _ := json.Marshal(cfg)
	return string(data)
}
