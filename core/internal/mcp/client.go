// Package mcp is Paladin's Model Context Protocol client: it launches
// or dials MCP servers (stdio and Streamable HTTP), keeps them alive,
// and exposes their tools to the agent loop.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ProtocolVersion is the MCP revision Paladin asks for; servers may
// answer with any of supportedVersions.
const ProtocolVersion = "2025-06-18"

var supportedVersions = map[string]bool{"2025-06-18": true, "2025-03-26": true, "2024-11-05": true}

// ClientVersion is reported to servers in clientInfo.
var ClientVersion = "0.7"

// ErrClosed is the connection error after Close.
var ErrClosed = errors.New("connection closed")

// Implementation is a server's self-description from initialize.
type Implementation struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// Annotations are a tool's behaviour hints. They are hints, not
// guarantees: the permission gate only uses them to skip a prompt.
type Annotations struct {
	Title           string `json:"title,omitempty"`
	ReadOnlyHint    *bool  `json:"readOnlyHint,omitempty"`
	DestructiveHint *bool  `json:"destructiveHint,omitempty"`
	IdempotentHint  *bool  `json:"idempotentHint,omitempty"`
	OpenWorldHint   *bool  `json:"openWorldHint,omitempty"`
}

// ReadOnly reports an explicit readOnlyHint.
func (a Annotations) ReadOnly() bool { return a.ReadOnlyHint != nil && *a.ReadOnlyHint }

// Destructive reports an explicit destructiveHint on a tool that is not
// read-only. The spec defaults it to true, but most servers never set
// it, so only an explicit hint counts.
func (a Annotations) Destructive() bool {
	return !a.ReadOnly() && a.DestructiveHint != nil && *a.DestructiveHint
}

// Tool is one tools/list entry.
type Tool struct {
	Name        string          `json:"name"`
	Title       string          `json:"title,omitempty"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
	Annotations Annotations     `json:"annotations,omitempty"`
}

// ResourceContents is an embedded resource body.
type ResourceContents struct {
	URI      string `json:"uri"`
	MimeType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
	Blob     string `json:"blob,omitempty"`
}

// Content is one tools/call content block.
type Content struct {
	Type     string            `json:"type"`
	Text     string            `json:"text,omitempty"`
	MimeType string            `json:"mimeType,omitempty"`
	Data     string            `json:"data,omitempty"`
	URI      string            `json:"uri,omitempty"`
	Name     string            `json:"name,omitempty"`
	Resource *ResourceContents `json:"resource,omitempty"`
}

// CallResult is a tools/call result.
type CallResult struct {
	Content           []Content       `json:"content"`
	StructuredContent json.RawMessage `json:"structuredContent,omitempty"`
	IsError           bool            `json:"isError,omitempty"`
}

// Text joins the text blocks.
func (r CallResult) Text() string {
	var parts []string
	for _, c := range r.Content {
		if c.Type == "text" {
			parts = append(parts, c.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// RPCError is a JSON-RPC error answer.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string { return fmt.Sprintf("%s (code %d)", e.Message, e.Code) }

// Options are client callbacks; all are optional and may be called
// from any goroutine.
type Options struct {
	// OnToolsChanged fires on notifications/tools/list_changed.
	OnToolsChanged func()
	// OnLog receives each stderr line and server log message.
	OnLog func(line string)
}

// transport moves raw JSON-RPC messages. Inbound messages go to the
// deliver callback given at construction; a dead transport calls fail
// exactly once.
type transport interface {
	send(ctx context.Context, msg []byte) error
	close() error
	pid() int
	setProtocolVersion(v string)
}

type response struct {
	result json.RawMessage
	err    *RPCError
}

// Client is one live MCP connection.
type Client struct {
	name   string
	tr     transport
	opts   Options
	stderr *tail

	nextID  atomic.Int64
	mu      sync.Mutex
	pending map[string]chan response
	done    chan struct{}
	err     error

	info         Implementation
	instructions string
	version      string
}

type wireMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// Connect starts (stdio) or dials (HTTP) the server and completes the
// initialize handshake under ctx. On failure everything it started is
// torn down and the error carries the server's stderr tail.
func Connect(ctx context.Context, name string, cfg ServerConfig, opts Options) (*Client, error) {
	c := &Client{name: name, opts: opts, stderr: newTail(16 << 10), pending: map[string]chan response{}, done: make(chan struct{})}
	var err error
	switch cfg.Transport() {
	case TransportStdio:
		c.tr, err = startStdio(cfg, c.dispatch, c.fail, c.logLine)
	case TransportHTTP:
		c.tr, err = newHTTP(cfg, c.dispatch, c.fail)
	default:
		err = fmt.Errorf("server %q: set either command (stdio) or url (http)", name)
	}
	if err != nil {
		return nil, err
	}
	if err := c.initialize(ctx); err != nil {
		_ = c.Close()
		if t := strings.TrimSpace(c.Stderr()); t != "" {
			return nil, fmt.Errorf("%w; stderr: %s", err, lastLines(t, 6))
		}
		return nil, err
	}
	return c, nil
}

func (c *Client) initialize(ctx context.Context) error {
	params := map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "paladin", "version": ClientVersion},
	}
	raw, err := c.request(ctx, "initialize", params)
	if err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	var res struct {
		ProtocolVersion string         `json:"protocolVersion"`
		ServerInfo      Implementation `json:"serverInfo"`
		Instructions    string         `json:"instructions"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("initialize: bad result: %w", err)
	}
	if !supportedVersions[res.ProtocolVersion] {
		return fmt.Errorf("initialize: unsupported protocol version %q", res.ProtocolVersion)
	}
	c.info, c.instructions, c.version = res.ServerInfo, res.Instructions, res.ProtocolVersion
	c.tr.setProtocolVersion(res.ProtocolVersion)
	return c.notify(ctx, "notifications/initialized", nil)
}

// ServerInfo is the server's initialize self-description.
func (c *Client) ServerInfo() Implementation { return c.info }

// Instructions are the server's optional usage notes for the model.
func (c *Client) Instructions() string { return c.instructions }

// Done is closed once the connection is dead (exit, I/O error, Close).
func (c *Client) Done() <-chan struct{} { return c.done }

// Err is why the connection died; nil while it is alive.
func (c *Client) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Stderr is the tail of the server's stderr and log messages.
func (c *Client) Stderr() string { return c.stderr.String() }

// PID is the stdio server's process id; 0 for HTTP.
func (c *Client) PID() int { return c.tr.pid() }

// Close ends the connection and stops a stdio server's process group.
func (c *Client) Close() error {
	err := c.tr.close()
	c.fail(ErrClosed)
	return err
}

// ListTools pages through tools/list.
func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	var all []Tool
	seen := map[string]bool{}
	cursor := ""
	for page := 0; page < 100; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := c.request(ctx, "tools/list", params)
		if err != nil {
			return nil, fmt.Errorf("tools/list: %w", err)
		}
		var res struct {
			Tools      []Tool `json:"tools"`
			NextCursor string `json:"nextCursor"`
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			return nil, fmt.Errorf("tools/list: bad result: %w", err)
		}
		for _, t := range res.Tools {
			if t.Name != "" && !seen[t.Name] {
				seen[t.Name] = true
				all = append(all, t)
			}
		}
		if res.NextCursor == "" || res.NextCursor == cursor {
			return all, nil
		}
		cursor = res.NextCursor
	}
	return all, nil
}

// CallTool runs tools/call. A JSON-RPC error answer (unknown tool, bad
// arguments) is a tool failure the model can act on, so it comes back
// as an error result; only transport problems and ctx return an error.
func (c *Client) CallTool(ctx context.Context, name string, args json.RawMessage) (CallResult, error) {
	if len(args) == 0 || string(args) == "null" {
		args = json.RawMessage("{}")
	}
	raw, err := c.request(ctx, "tools/call", map[string]any{"name": name, "arguments": args})
	var rpcErr *RPCError
	if errors.As(err, &rpcErr) {
		return CallResult{IsError: true, Content: []Content{{Type: "text", Text: rpcErr.Message}}}, nil
	}
	if err != nil {
		return CallResult{}, err
	}
	var res CallResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return CallResult{}, fmt.Errorf("tools/call: bad result: %w", err)
	}
	return res, nil
}

// Ping checks the server answers.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.request(ctx, "ping", nil)
	return err
}

func (c *Client) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if err := c.Err(); err != nil {
		return nil, err
	}
	id := c.nextID.Add(1)
	key := strconv.FormatInt(id, 10)
	ch := make(chan response, 1)
	c.mu.Lock()
	c.pending[key] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, key)
		c.mu.Unlock()
	}()
	msg, err := json.Marshal(wireMessage{JSONRPC: "2.0", ID: json.RawMessage(key), Method: method, Params: marshalParams(params)})
	if err != nil {
		return nil, err
	}
	if err := c.tr.send(ctx, msg); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if derr := c.Err(); derr != nil {
			return nil, derr
		}
		return nil, err
	}
	select {
	case r := <-ch:
		if r.err != nil {
			return nil, r.err
		}
		return r.result, nil
	case <-ctx.Done():
		if method != "initialize" {
			c.cancelRequest(key, ctx.Err())
		}
		return nil, ctx.Err()
	case <-c.done:
		// A reply may have raced the death; prefer it.
		select {
		case r := <-ch:
			if r.err != nil {
				return nil, r.err
			}
			return r.result, nil
		default:
		}
		return nil, c.Err()
	}
}

func (c *Client) cancelRequest(key string, why error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	reason := "cancelled"
	if errors.Is(why, context.DeadlineExceeded) {
		reason = "timed out"
	}
	_ = c.notify(ctx, "notifications/cancelled", map[string]any{"requestId": json.RawMessage(key), "reason": reason})
}

func (c *Client) notify(ctx context.Context, method string, params any) error {
	msg, err := json.Marshal(wireMessage{JSONRPC: "2.0", Method: method, Params: marshalParams(params)})
	if err != nil {
		return err
	}
	return c.tr.send(ctx, msg)
}

func marshalParams(params any) json.RawMessage {
	if params == nil {
		return nil
	}
	raw, _ := json.Marshal(params)
	return raw
}

// dispatch routes one inbound message; transports call it.
func (c *Client) dispatch(raw []byte) {
	var m wireMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		c.logLine("unparseable message from server: " + truncate(string(raw), 200))
		return
	}
	switch {
	case m.Method == "" && len(m.ID) > 0:
		c.mu.Lock()
		ch := c.pending[idKey(m.ID)]
		c.mu.Unlock()
		if ch != nil {
			ch <- response{result: m.Result, err: m.Error}
		}
	case m.Method != "" && len(m.ID) > 0:
		go c.answer(m)
	case m.Method == "notifications/tools/list_changed":
		if c.opts.OnToolsChanged != nil {
			go c.opts.OnToolsChanged()
		}
	case m.Method == "notifications/message":
		var p struct {
			Level string          `json:"level"`
			Data  json.RawMessage `json:"data"`
		}
		_ = json.Unmarshal(m.Params, &p)
		c.logLine("[" + p.Level + "] " + truncate(string(p.Data), 500))
	}
}

// idKey normalizes a JSON-RPC id so 7 and "7" match ours.
func idKey(id json.RawMessage) string {
	var s string
	if json.Unmarshal(id, &s) == nil {
		return s
	}
	return strings.TrimSpace(string(id))
}

// answer replies to a server-initiated request. Paladin declares no
// client capabilities, so only ping is served.
func (c *Client) answer(m wireMessage) {
	reply := wireMessage{JSONRPC: "2.0", ID: m.ID}
	if m.Method == "ping" {
		reply.Result = json.RawMessage("{}")
	} else {
		reply.Error = &RPCError{Code: -32601, Message: "method not found: " + m.Method}
	}
	msg, _ := json.Marshal(reply)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = c.tr.send(ctx, msg)
}

func (c *Client) fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return
	}
	if err == nil {
		err = ErrClosed
	}
	c.err = err
	close(c.done)
}

func (c *Client) logLine(line string) {
	c.stderr.WriteLine(line)
	if c.opts.OnLog != nil {
		c.opts.OnLog(line)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " | ")
}

// tail keeps the last max bytes of line-oriented output.
type tail struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func newTail(max int) *tail { return &tail{max: max} }

func (t *tail) WriteLine(line string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, line...)
	t.buf = append(t.buf, '\n')
	if over := len(t.buf) - t.max; over > 0 {
		cut := over
		if i := strings.IndexByte(string(t.buf[over:]), '\n'); i >= 0 {
			cut = over + i + 1
		}
		t.buf = append([]byte(nil), t.buf[cut:]...)
	}
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}
