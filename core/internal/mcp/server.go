package mcp

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// server is one configured server and its current connection. gen
// counts connection attempts so a stale attempt or watcher can tell it
// was superseded.
type server struct {
	m       *Manager
	key     serverKey
	cfg     ServerConfig
	sig     string
	trusted bool

	mu      sync.Mutex
	removed bool
	state   string
	err     string
	crashed bool
	gen     int
	ready   chan struct{}
	client  *Client
	last    *Client
	// prevStderr is the tail of earlier processes' stderr, kept across
	// restarts so a crash still explains itself.
	prevStderr   string
	tools        []Tool
	info         Implementation
	instructions string
	restarts     int
	crashes      []time.Time
}

var closedChan = func() chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}()

func newServer(m *Manager, k serverKey, cfg ServerConfig, sig string, trusted bool) *server {
	s := &server{m: m, key: k, cfg: cfg, sig: sig, trusted: trusted, state: StateStopped}
	switch {
	case !cfg.IsEnabled():
		s.state = StateDisabled
	case !trusted:
		s.state = StateUntrusted
	}
	return s
}

// runnable: enabled and, for folder servers, trusted.
func (s *server) runnable() bool { return s.cfg.IsEnabled() && s.trusted }

// ensure starts the server if it should be running and returns a
// channel closed once the current attempt settles; nil if it can't run.
func (s *server) ensure() <-chan struct{} {
	s.mu.Lock()
	if s.removed || !s.runnable() {
		s.mu.Unlock()
		return nil
	}
	switch s.state {
	case StateReady:
		s.mu.Unlock()
		return closedChan
	case StateStarting:
		ch := s.ready
		s.mu.Unlock()
		return ch
	case StateFailed:
		// Startup failures wait for a config change or manual restart;
		// a crash after a good start is retried within the budget.
		if !s.crashed {
			s.mu.Unlock()
			return closedChan
		}
		now := time.Now()
		s.crashes = slices.DeleteFunc(s.crashes, func(t time.Time) bool { return now.Sub(t) > crashWindow })
		if len(s.crashes) >= crashBudget {
			s.err = fmt.Sprintf("crashed %d times in %v; restart it from Settings → MCP", len(s.crashes), crashWindow)
			s.crashed = false
			s.mu.Unlock()
			s.m.publish(s.status())
			return closedChan
		}
		s.restarts++
	}
	ch := s.startLocked()
	s.mu.Unlock()
	s.m.publish(s.status())
	return ch
}

func (s *server) startLocked() chan struct{} {
	s.gen++
	s.state, s.err, s.crashed = StateStarting, "", false
	s.ready = make(chan struct{})
	go s.connect(s.gen, s.ready)
	return s.ready
}

func (s *server) connect(gen int, ready chan struct{}) {
	defer close(ready)
	cfg := expand(s.cfg, s.key.folder)
	ctx, cancel := context.WithTimeout(s.m.ctx, cfg.StartupTimeout())
	defer cancel()
	c, err := Connect(ctx, s.key.name, cfg, Options{OnToolsChanged: func() { go s.relist(gen) }})
	var list []Tool
	if err == nil {
		if list, err = c.ListTools(ctx); err != nil {
			_ = c.Close()
			err = fmt.Errorf("tools/list: %w", err)
		}
	}
	if err != nil && ctx.Err() == context.DeadlineExceeded && s.m.ctx.Err() == nil {
		err = fmt.Errorf("did not start within %v: %w", cfg.StartupTimeout(), err)
	}
	s.mu.Lock()
	if gen != s.gen || s.removed {
		s.mu.Unlock()
		if c != nil {
			_ = c.Close()
		}
		return
	}
	if err != nil {
		s.state, s.err = StateFailed, err.Error()
		s.mu.Unlock()
		s.m.publish(s.status())
		return
	}
	if s.last != nil {
		if old := s.last.Stderr(); old != "" {
			if !strings.HasSuffix(old, "\n") {
				old += "\n"
			}
			s.prevStderr = tailString(s.prevStderr+old+"--- restarted ---\n", maxStatusStderr)
		}
	}
	s.client, s.last, s.tools = c, c, list
	s.info, s.instructions = c.ServerInfo(), c.Instructions()
	s.state = StateReady
	s.mu.Unlock()
	go s.watch(gen, c)
	s.m.publish(s.status())
}

// watch marks the server failed when its connection dies on its own.
func (s *server) watch(gen int, c *Client) {
	<-c.Done()
	s.mu.Lock()
	if gen != s.gen || s.client != c {
		s.mu.Unlock()
		return
	}
	s.client = nil
	s.state, s.crashed = StateFailed, true
	if err := c.Err(); err != nil {
		s.err = err.Error()
	}
	s.crashes = append(s.crashes, time.Now())
	s.mu.Unlock()
	s.m.publish(s.status())
}

func (s *server) relist(gen int) {
	s.mu.Lock()
	c := s.client
	ok := gen == s.gen && c != nil
	s.mu.Unlock()
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(s.m.ctx, s.cfg.StartupTimeout())
	defer cancel()
	list, err := c.ListTools(ctx)
	if err != nil {
		return
	}
	s.mu.Lock()
	if gen != s.gen || s.client != c {
		s.mu.Unlock()
		return
	}
	s.tools = list
	s.mu.Unlock()
	s.m.publish(s.status())
}

// acquire returns a live client, (re)starting the server if allowed.
func (s *server) acquire(ctx context.Context) (*Client, error) {
	ch := s.ensure()
	if ch == nil {
		return nil, fmt.Errorf("MCP server %q is no longer configured", s.key.name)
	}
	select {
	case <-ch:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client == nil {
		return nil, fmt.Errorf("MCP server %q is unavailable: %s", s.key.name, s.err)
	}
	return s.client, nil
}

// reset stops the connection and clears failure state.
func (s *server) reset() {
	s.mu.Lock()
	s.gen++
	c := s.client
	s.client = nil
	s.state, s.err, s.crashed, s.crashes = StateStopped, "", false, nil
	s.mu.Unlock()
	if c != nil {
		_ = c.Close()
	}
}

// remove stops the server for good.
func (s *server) remove() {
	s.mu.Lock()
	s.removed = true
	s.gen++
	c := s.client
	s.client = nil
	s.state = StateStopped
	s.mu.Unlock()
	if c != nil {
		_ = c.Close()
	}
}

func (s *server) filtered() []Tool {
	var out []Tool
	for _, t := range s.tools {
		if len(s.cfg.AllowedTools) > 0 && !slices.Contains(s.cfg.AllowedTools, t.Name) {
			continue
		}
		if slices.Contains(s.cfg.DeniedTools, t.Name) {
			continue
		}
		out = append(out, t)
	}
	return out
}

func (s *server) status() ServerStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := ServerStatus{
		Name:      s.key.name,
		Scope:     s.key.scope,
		Folder:    s.key.folder,
		Transport: s.cfg.Transport(),
		State:     s.state,
		Error:     s.err,
		Restarts:  s.restarts,
		Tools:     []ToolStatus{},
	}
	if s.last != nil {
		st.Stderr = tailString(s.prevStderr+s.last.Stderr(), maxStatusStderr)
	}
	if s.state == StateReady {
		info := s.info
		st.Server = &info
		for _, t := range s.filtered() {
			st.Tools = append(st.Tools, ToolStatus{
				Name:        t.Name,
				Exposed:     exposedName(s.key.name, t.Name),
				Description: t.Description,
				ReadOnly:    t.Annotations.ReadOnly(),
				Destructive: t.Annotations.Destructive(),
			})
		}
	}
	return st
}

func (s *server) wrappers() []*mcpTool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != StateReady {
		return nil
	}
	var out []*mcpTool
	for _, t := range s.filtered() {
		out = append(out, &mcpTool{srv: s, tool: t, name: exposedName(s.key.name, t.Name)})
	}
	return out
}

func tailString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	i := len(s) - n
	for i < len(s) && !utf8.RuneStart(s[i]) {
		i++
	}
	return s[i:]
}
