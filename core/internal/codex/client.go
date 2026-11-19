// Package codex drives the OpenAI Codex CLI through its `app-server`
// JSON-RPC protocol (one JSON object per line on stdio). Paladin spawns one
// long-lived app-server, starts Codex threads on it, and turns each thread's
// notifications and approval requests into Paladin turn events (see the
// runtime package). Codex keeps its own login, models, sandbox and tools.
package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// BinEnv overrides the codex executable (tests point it at a fake).
const BinEnv = "PALADIN_CODEX_BIN"

// ErrClosed is returned once the app-server process is gone.
var ErrClosed = errors.New("codex app-server is not running")

// RPCError is an error answer from the app-server.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string { return fmt.Sprintf("codex: %s (code %d)", e.Message, e.Code) }

// Inbound is one thread-scoped message from the app-server: a notification,
// or a request that needs an answer through Client.Respond (RequestID set).
type Inbound struct {
	Method    string
	Params    json.RawMessage
	RequestID json.RawMessage
}

// IsRequest reports whether the message waits for a Respond call.
func (m Inbound) IsRequest() bool { return len(m.RequestID) > 0 }

type wireMessage struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *RPCError       `json:"error,omitempty"`
}

type reply struct {
	result json.RawMessage
	err    error
}

// Client is one running `codex app-server` process.
type Client struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser

	writeMu sync.Mutex
	nextID  atomic.Int64

	mu      sync.Mutex
	pending map[int64]chan reply
	subs    map[string]*Subscription
	loaded  map[string]bool
	models  map[string]string // the model Codex resolved for each thread
	notices []string          // account-level warnings that belong to no thread yet
	closed  bool

	done    chan struct{}
	exitErr error

	reqOnce      sync.Once
	requirements Requirements
	stderr       *tail

	// Info is what `initialize` answered (codex home, platform).
	Info InitializeInfo
}

// InitializeInfo is the app-server's answer to `initialize`.
type InitializeInfo struct {
	UserAgent string `json:"userAgent"`
	CodexHome string `json:"codexHome"`
}

// applicationDirs lists where desktop apps are installed (a variable so tests
// can point it elsewhere).
var applicationDirs = func(home string) []string {
	dirs := []string{"/Applications"}
	if home != "" {
		dirs = append(dirs, home+"/Applications")
	}
	return dirs
}

// bundledCLI is where a desktop app keeps its own copy of the CLI, under the
// app's folder: the Codex desktop app, now shipped inside ChatGPT.app, carries
// one and puts nothing on PATH.
var bundledCLI = []string{"Contents/Resources/codex-cli/bin/codex", "Contents/Resources/codex"}

// FindBinary locates the codex executable: PALADIN_CODEX_BIN, then PATH, then the
// PATH of the user's own shell (where pnpm, nvm or Volta put it), then the places
// GUI apps usually find it, then the CLI bundled inside an installed Codex or
// ChatGPT desktop app.
func FindBinary() (string, error) {
	if bin := strings.TrimSpace(os.Getenv(BinEnv)); bin != "" {
		path, err := exec.LookPath(bin)
		if err != nil {
			return "", fmt.Errorf("codex CLI unavailable (%s): %w", BinEnv, err)
		}
		return path, nil
	}
	if path, err := exec.LookPath("codex"); err == nil {
		return path, nil
	}
	// The user's own shell knows where their install is (pnpm, nvm, Volta).
	if path, ok := lookPathIn("codex", shellPATH()); ok {
		return path, nil
	}
	home, _ := os.UserHomeDir()
	candidates := []string{"/usr/local/bin/codex", "/opt/homebrew/bin/codex", "/usr/bin/codex"}
	if home != "" {
		candidates = append(candidates,
			home+"/.local/bin/codex", home+"/.codex/bin/codex", home+"/.npm-global/bin/codex",
			home+"/.bun/bin/codex", home+"/.volta/bin/codex")
	}
	for _, dir := range applicationDirs(home) {
		for _, rel := range bundledCLI {
			// Glob returns the matches sorted, so the choice is the same every time.
			matches, _ := filepath.Glob(filepath.Join(dir, "*.app", rel))
			candidates = append(candidates, matches...)
		}
	}
	for _, c := range candidates {
		if path, err := exec.LookPath(c); err == nil {
			return path, nil
		}
	}
	return "", errors.New("codex CLI not found: install the Codex app or the CLI (npm i -g @openai/codex) and run `codex login`")
}

// Start launches `codex app-server` and completes the initialize handshake.
func Start(ctx context.Context, bin string) (*Client, error) {
	cmd := exec.Command(bin, "app-server")
	cmd.Env = childEnv()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr := &tail{max: 4096}
	cmd.Stderr = stderr
	setProcAttr(cmd)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start codex: %w", err)
	}
	c := &Client{
		cmd: cmd, stdin: stdin, stderr: stderr,
		pending: map[int64]chan reply{}, subs: map[string]*Subscription{}, loaded: map[string]bool{}, models: map[string]string{},
		done: make(chan struct{}),
	}
	go c.read(stdout)
	go func() {
		err := cmd.Wait()
		c.fail(fmt.Errorf("codex app-server exited: %w%s", err, c.stderr.suffix()))
	}()

	hctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	params := map[string]any{
		"clientInfo":   map[string]any{"name": "paladin", "title": "Paladin", "version": "1"},
		"capabilities": map[string]any{"experimentalApi": true},
	}
	if err := c.Call(hctx, "initialize", params, &c.Info); err != nil {
		c.Close()
		return nil, err
	}
	if err := c.notify("initialized"); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

// Done is closed when the process has exited.
func (c *Client) Done() <-chan struct{} { return c.done }

// Err is why the process exited, once Done is closed.
func (c *Client) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.exitErr
}

// Close stops the app-server.
func (c *Client) Close() {
	c.fail(ErrClosed)
	if c.cmd.Process != nil {
		killGroup(c.cmd)
	}
}

func (c *Client) fail(err error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.exitErr = err
	pending := c.pending
	c.pending = map[int64]chan reply{}
	subs := c.subs
	c.subs = map[string]*Subscription{}
	c.mu.Unlock()
	close(c.done)
	for _, ch := range pending {
		ch <- reply{err: err}
	}
	for _, s := range subs {
		s.close()
	}
	_ = c.stdin.Close()
}

func (c *Client) read(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var m wireMessage
		if err := json.Unmarshal(line, &m); err != nil {
			continue
		}
		c.route(m)
	}
}

func (c *Client) route(m wireMessage) {
	if m.Method == "" {
		var id int64
		if json.Unmarshal(m.ID, &id) != nil {
			return
		}
		c.mu.Lock()
		ch, ok := c.pending[id]
		delete(c.pending, id)
		c.mu.Unlock()
		if !ok {
			return
		}
		if m.Error != nil {
			ch <- reply{err: m.Error}
			return
		}
		ch <- reply{result: m.Result}
		return
	}
	var head struct {
		ThreadID string `json:"threadId"`
	}
	_ = json.Unmarshal(m.Params, &head)
	in := Inbound{Method: m.Method, Params: m.Params}
	if len(m.ID) > 0 {
		in.RequestID = m.ID
	}
	if head.ThreadID == "" && !in.IsRequest() {
		c.noteGlobal(in)
	}
	c.mu.Lock()
	sub := c.subs[head.ThreadID]
	c.mu.Unlock()
	if sub != nil && head.ThreadID != "" {
		sub.push(in)
		return
	}
	if in.IsRequest() {
		// Nobody owns this request; refuse it so Codex does not wait.
		_ = c.RespondError(in.RequestID, -32601, "paladin does not handle "+m.Method)
	}
}

// Call sends a request and decodes its result into out (nil to ignore it).
func (c *Client) Call(ctx context.Context, method string, params, out any) error {
	id := c.nextID.Add(1)
	ch := make(chan reply, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrClosed
	}
	c.pending[id] = ch
	c.mu.Unlock()
	err := c.write(map[string]any{"id": id, "method": method, "params": params})
	if err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return err
	}
	select {
	case r := <-ch:
		if r.err != nil {
			return r.err
		}
		if out != nil && len(r.result) > 0 {
			if err := json.Unmarshal(r.result, out); err != nil {
				return fmt.Errorf("codex %s: decode result: %w", method, err)
			}
		}
		return nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return ctx.Err()
	}
}

func (c *Client) notify(method string) error {
	return c.write(map[string]any{"method": method})
}

// Respond answers a server request received as an Inbound.
func (c *Client) Respond(id json.RawMessage, result any) error {
	return c.write(map[string]any{"id": id, "result": result})
}

// RespondError refuses a server request.
func (c *Client) RespondError(id json.RawMessage, code int, message string) error {
	return c.write(map[string]any{"id": id, "error": map[string]any{"code": code, "message": message}})
}

func (c *Client) write(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	select {
	case <-c.done:
		return ErrClosed
	default:
	}
	_, err = c.stdin.Write(append(data, '\n'))
	return err
}

// Loaded reports whether this process already knows the thread (it was
// started or resumed here), and MarkLoaded records that it does.
func (c *Client) Loaded(threadID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.loaded[threadID]
}

// ThreadModel is the model Codex resolved for a thread when it started or
// resumed it: the one asked for, or Codex's own default when none was.
func (c *Client) ThreadModel(threadID string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.models[threadID]
}

func (c *Client) noteModel(threadID, model string) {
	if model == "" {
		return
	}
	c.mu.Lock()
	c.models[threadID] = model
	c.mu.Unlock()
}

// MarkLoaded records that threadID is live in this process.
func (c *Client) MarkLoaded(threadID string) {
	c.mu.Lock()
	c.loaded[threadID] = true
	c.mu.Unlock()
}

// Subscribe routes every message about threadID to the returned
// subscription until it is closed. One subscriber per thread.
func (c *Client) Subscribe(threadID string) (*Subscription, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, ErrClosed
	}
	if c.subs[threadID] != nil {
		return nil, fmt.Errorf("codex thread %s already has a running turn", threadID)
	}
	s := newSubscription(func() {
		c.mu.Lock()
		delete(c.subs, threadID)
		c.mu.Unlock()
	})
	c.subs[threadID] = s
	return s, nil
}

// Subscription is an unbounded queue of one thread's messages, so a slow
// consumer never blocks the process's stdout reader.
type Subscription struct {
	mu      sync.Mutex
	queue   []Inbound
	wake    chan struct{}
	closed  bool
	release func()
}

func newSubscription(release func()) *Subscription {
	return &Subscription{wake: make(chan struct{}, 1), release: release}
}

func (s *Subscription) push(m Inbound) {
	s.mu.Lock()
	if !s.closed {
		s.queue = append(s.queue, m)
	}
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Subscription) close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Close stops delivery and frees the thread for another subscriber.
func (s *Subscription) Close() {
	s.close()
	if s.release != nil {
		s.release()
	}
}

// Peek returns the oldest message without removing it.
func (s *Subscription) Peek() (m Inbound, ok, closed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queue) > 0 {
		return s.queue[0], true, false
	}
	return Inbound{}, false, s.closed
}

// Pop drops the oldest message.
func (s *Subscription) Pop() {
	s.mu.Lock()
	if len(s.queue) > 0 {
		s.queue = s.queue[1:]
	}
	s.mu.Unlock()
}

// Done reports that the subscription ended and everything queued was read.
func (s *Subscription) Done() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed && len(s.queue) == 0
}

// Wake is signalled whenever Next may have something to return.
func (s *Subscription) Wake() <-chan struct{} { return s.wake }

// Next pops the oldest message; ok is false when none is queued. Closed
// reports that the subscription ended (the process died or Close ran) and
// is drained.
func (s *Subscription) Next() (m Inbound, ok, closed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queue) > 0 {
		m = s.queue[0]
		s.queue = s.queue[1:]
		return m, true, false
	}
	return Inbound{}, false, s.closed
}

// tail keeps the last bytes written to it, for exit diagnostics.
type tail struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(p), nil
}

func (t *tail) suffix() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := strings.TrimSpace(string(t.buf))
	if s == "" {
		return ""
	}
	return ": " + s
}

// noteGlobal keeps a warning Codex raises about the account rather than a
// thread (a broken config, a deprecated setting), for the next turn to show.
func (c *Client) noteGlobal(in Inbound) {
	p := Decode(in.Params)
	var text string
	switch in.Method {
	case "configWarning":
		text = p.Str("summary")
		if d := p.Str("details"); d != "" {
			text += ": " + d
		}
		if path := p.Str("path"); path != "" {
			text += " (" + path + ")"
		}
	case "deprecationNotice":
		text = p.Str("summary")
		if d := p.Str("details"); d != "" {
			text += ": " + d
		}
	case "warning":
		text = p.Str("message")
	}
	if strings.TrimSpace(text) == "" {
		return
	}
	c.mu.Lock()
	if len(c.notices) < 20 {
		c.notices = append(c.notices, text)
	}
	c.mu.Unlock()
}

// TakeNotices returns the account-level warnings not shown yet, once each.
func (c *Client) TakeNotices() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.notices
	c.notices = nil
	return out
}
