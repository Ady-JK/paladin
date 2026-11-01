// manager.go owns the running servers: one per server kind and project
// root, started on first use, shut down after a quiet spell, and kept
// off for a minute after one fails to start so a broken install does
// not slow every edit.

package lsp

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Ady-JK/paladin/core/internal/tools"
)

const (
	defaultTimeout = 1500 * time.Millisecond
	maxTimeout     = 10 * time.Second
	// readyWait bounds how long one edit waits for a server that is
	// still starting (loading the project).
	readyWait       = 10 * time.Second
	idleAfter       = 10 * time.Minute
	brokenFor       = time.Minute
	maxReport       = 20
	maxMessageRunes = 300
)

// Manager runs language servers for the agent.
type Manager struct {
	dataDir string
	mu      sync.Mutex
	servers map[string]*server
	broken  map[string]brokenEntry
	done    chan struct{}
	once    sync.Once
	// closed is set by Close under mu; after it nothing starts.
	closed bool
	// warming counts Warm goroutines; ctx cancels them on Close.
	warming sync.WaitGroup
	ctx     context.Context
	cancel  context.CancelFunc
	// idle is idleAfter; tests shorten it.
	idle time.Duration
}

type brokenEntry struct {
	err   string
	until time.Time
}

// Result is what a diagnose call found.
type Result struct {
	// Server is the server id ("" when no server handles the file).
	Server      string
	Diagnostics []tools.Diagnostic
	// Errors counts every error the server reported (Diagnostics may be
	// capped).
	Errors int
	// Timedout is true when the server did not answer in time.
	Timedout bool
}

// ServerStatus describes one server for the UI.
type ServerStatus struct {
	ID    string `json:"id"`
	Root  string `json:"root"`
	State string `json:"state"` // starting, ready, failed
	PID   int    `json:"pid,omitempty"`
	Error string `json:"error,omitempty"`
}

func NewManager(dataDir string) *Manager {
	m := &Manager{dataDir: dataDir, servers: map[string]*server{}, broken: map[string]brokenEntry{},
		done: make(chan struct{}), idle: idleAfter}
	m.ctx, m.cancel = context.WithCancel(context.Background())
	go m.reap()
	return m
}

func key(id, root string) string { return id + "\x00" + root }

// pick finds the server for path: its spec, project root and binary.
// trust (nil trusts) decides whether a binary the workspace ships, or a
// server that would run the project's own code, may start.
func (m *Manager) pick(workspace, path string, trust func(string) bool) (Spec, string, string, bool) {
	specs, _, _ := loadSpecs(m.dataDir)
	dir := filepath.Dir(path)
	for _, s := range candidates(specs, path) {
		bin, ok := resolveBinary(s.Command[0], dir, workspace, trust)
		if !ok {
			continue
		}
		root := projectRoot(dir, workspace, s.RootMarkers)
		if label := runsProjectCode(s, root); label != "" && trust != nil && !trust(label) {
			continue
		}
		return s, root, bin, true
	}
	return Spec{}, "", "", false
}

// runsProjectCode names a server that runs code from the project even
// when installed elsewhere: rust-analyzer builds build.rs and proc
// macros, typescript-language-server loads the project's own TypeScript.
func runsProjectCode(s Spec, root string) string {
	switch s.ID {
	case "rust-analyzer":
		return "rust-analyzer (runs this project's build scripts and proc macros)"
	case "typescript":
		if st, err := os.Stat(filepath.Join(root, "node_modules", "typescript")); err == nil && st.IsDir() {
			return "typescript-language-server with node_modules/typescript"
		}
	}
	return ""
}

// get returns the live server for spec at root, starting one if needed.
func (m *Manager) get(spec Spec, root, bin string) (*server, error) {
	k := key(spec.ID, root)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, errClosed
	}
	if s := m.servers[k]; s != nil && s.alive() {
		return s, nil
	}
	if b, ok := m.broken[k]; ok && time.Now().Before(b.until) {
		return nil, errString(b.err)
	}
	s, err := startServer(spec, root, bin)
	if err != nil {
		m.broken[k] = brokenEntry{err: err.Error(), until: time.Now().Add(brokenFor)}
		return nil, err
	}
	delete(m.broken, k)
	m.servers[k] = s
	return s, nil
}

func (m *Manager) markBroken(spec Spec, root string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := key(spec.ID, root)
	m.broken[k] = brokenEntry{err: err.Error(), until: time.Now().Add(brokenFor)}
	if s := m.servers[k]; s != nil {
		delete(m.servers, k)
		go s.stop()
	}
}

// Warm starts the server for path (if any) and opens the file, without
// waiting, so the first edit finds it ready. Files over maxDocBytes are
// skipped.
func (m *Manager) Warm(workspace, path string, trust func(string) bool) {
	if workspace == "" || !tools.Within(path, workspace) || !fitsDoc(path) {
		return
	}
	spec, root, bin, ok := m.pick(workspace, path, trust)
	if !ok {
		return
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.warming.Add(1)
	m.mu.Unlock()
	go func() {
		defer m.warming.Done()
		s, err := m.get(spec, root, bin)
		if err != nil {
			return
		}
		ctx, cancel := context.WithTimeout(m.ctx, readyWait*3)
		defer cancel()
		if err := s.waitReady(ctx); err != nil {
			if ctx.Err() == nil {
				m.markBroken(spec, root, err)
			}
			return
		}
		s.open(path)
	}()
}

// Diagnose sends path's current content to its server and returns the
// errors it reports, waiting at most the configured timeout once the
// server is ready. Files over maxDocBytes are not checked.
func (m *Manager) Diagnose(ctx context.Context, workspace, path string, trust func(string) bool) Result {
	if workspace == "" || !tools.Within(path, workspace) || !fitsDoc(path) {
		return Result{}
	}
	spec, root, bin, ok := m.pick(workspace, path, trust)
	if !ok {
		return Result{}
	}
	res := Result{Server: spec.ID}
	s, err := m.get(spec, root, bin)
	if err != nil {
		return res
	}
	rctx, cancel := context.WithTimeout(ctx, readyWait)
	err = s.waitReady(rctx)
	cancel()
	if err != nil {
		if ctx.Err() == nil && rctx.Err() == nil {
			m.markBroken(spec, root, err)
		}
		res.Timedout = true
		return res
	}
	diags, ok := s.diagnose(ctx, path, m.timeout())
	if !ok {
		res.Timedout = true
		return res
	}
	for _, d := range diags {
		if d.Severity != SeverityError && d.Severity != 0 {
			continue
		}
		res.Errors++
		if len(res.Diagnostics) == maxReport {
			continue
		}
		msg := strings.TrimSpace(d.Message)
		if r := []rune(msg); len(r) > maxMessageRunes {
			msg = string(r[:maxMessageRunes]) + "…"
		}
		res.Diagnostics = append(res.Diagnostics, tools.Diagnostic{
			Line: d.Range.Start.Line + 1, Column: d.Range.Start.Character + 1,
			Message: msg, Source: firstNonEmpty(d.Source, spec.ID), Code: codeText(d.Code),
		})
	}
	sort.SliceStable(res.Diagnostics, func(i, j int) bool {
		a, b := res.Diagnostics[i], res.Diagnostics[j]
		return a.Line < b.Line || (a.Line == b.Line && a.Column < b.Column)
	})
	return res
}

func (m *Manager) timeout() time.Duration {
	_, ms, _ := loadSpecs(m.dataDir)
	if ms <= 0 {
		return defaultTimeout
	}
	return min(time.Duration(ms)*time.Millisecond, maxTimeout)
}

// Status lists running and recently failed servers.
func (m *Manager) Status() []ServerStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []ServerStatus{}
	for _, s := range m.servers {
		st := ServerStatus{ID: s.spec.ID, Root: s.root, PID: s.pid(), State: "starting"}
		select {
		case <-s.ready:
			st.State = "ready"
			if s.initErr != nil {
				st.State, st.Error = "failed", s.initErr.Error()
			}
		default:
		}
		if !s.alive() {
			st.State, st.Error, st.PID = "failed", firstNonEmpty(strings.TrimSpace(s.stderrTail()), "exited"), 0
		}
		out = append(out, st)
	}
	for k, b := range m.broken {
		if time.Now().After(b.until) {
			continue
		}
		id, root, _ := strings.Cut(k, "\x00")
		if _, running := m.servers[k]; running {
			continue
		}
		out = append(out, ServerStatus{ID: id, Root: root, State: "failed", Error: b.err})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID+out[i].Root < out[j].ID+out[j].Root })
	return out
}

// reap stops servers nobody used for a while.
func (m *Manager) reap() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-m.done:
			return
		case <-t.C:
			m.stopIdle()
		}
	}
}

func (m *Manager) stopIdle() {
	m.mu.Lock()
	var stale []*server
	for k, s := range m.servers {
		s.mu.Lock()
		quiet := time.Since(s.lastUse) > m.idle
		s.mu.Unlock()
		if quiet || !s.alive() {
			stale = append(stale, s)
			delete(m.servers, k)
		}
	}
	m.mu.Unlock()
	for _, s := range stale {
		s.stop()
	}
}

// Close stops every server and waits for warm-ups; the manager starts
// nothing afterwards.
func (m *Manager) Close() {
	m.once.Do(func() { close(m.done) })
	m.cancel()
	m.mu.Lock()
	m.closed = true
	all := make([]*server, 0, len(m.servers))
	for k, s := range m.servers {
		all = append(all, s)
		delete(m.servers, k)
	}
	m.mu.Unlock()
	var wg sync.WaitGroup
	for _, s := range all {
		wg.Add(1)
		go func() { defer wg.Done(); s.stop() }()
	}
	wg.Wait()
	// A warm-up past get() holds a server that is now stopped; wait so
	// none is still writing to it when Close returns.
	m.warming.Wait()
}

type errString string

func (e errString) Error() string { return string(e) }

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func codeText(raw []byte) string {
	s := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	if s == "null" {
		return ""
	}
	return s
}
