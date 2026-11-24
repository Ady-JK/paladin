// Package claude drives the user's Claude Code CLI over its bidirectional
// stream-json protocol. It does not embed an SDK or manage authentication.
package claude

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

const BinEnv = "PALADIN_CLAUDE_BIN"
const maxFrame = 32 * 1024 * 1024

var ErrClosed = errors.New("claude Code is not running")

type Raw map[string]json.RawMessage

func Decode(b []byte) Raw {
	var r Raw
	if json.Unmarshal(b, &r) != nil || r == nil {
		return Raw{}
	}
	return r
}
func (r Raw) Str(k string) string       { var v string; _ = json.Unmarshal(r[k], &v); return v }
func (r Raw) Obj(k string) Raw          { return Decode(r[k]) }
func (r Raw) Bool(k string) bool        { var v bool; _ = json.Unmarshal(r[k], &v); return v }
func (r Raw) Int(k string) int64        { var v float64; _ = json.Unmarshal(r[k], &v); return int64(v) }
func (r Raw) Float(k string) float64    { var v float64; _ = json.Unmarshal(r[k], &v); return v }
func (r Raw) List(k string) []Raw       { var v []Raw; _ = json.Unmarshal(r[k], &v); return v }
func (r Raw) Strings(k string) []string { var v []string; _ = json.Unmarshal(r[k], &v); return v }
func (r Raw) JSON(k string) string {
	if b := r[k]; len(b) > 0 {
		return string(b)
	}
	return "null"
}

func FindBinary() (string, error) {
	if p := os.Getenv(BinEnv); p != "" {
		return exec.LookPath(p)
	}
	if p, err := exec.LookPath("claude"); err == nil {
		return p, nil
	}
	home, _ := os.UserHomeDir()
	name := "claude"
	if os.PathSeparator == '\\' {
		name += ".exe"
	}
	p := filepath.Join(home, ".local", "bin", name)
	if st, err := os.Stat(p); err == nil && !st.IsDir() {
		return p, nil
	}
	return "", errors.New("claude Code is not installed: install the Claude Code CLI and sign in with `claude auth login`")
}

type Options struct {
	Cwd, SessionID, Resume, Model, Effort, PermissionMode, SystemPrompt string
	MaxBudget                                                           float64
	ForkSession                                                         bool
	ResumeAt                                                            string
	// Hooks registers host callbacks before any tools can run.
	Hooks map[string]any
}
type Model struct {
	ID          string   `json:"id"`
	DisplayName string   `json:"displayName"`
	Description string   `json:"description,omitempty"`
	Efforts     []string `json:"efforts"`
	IsDefault   bool     `json:"isDefault"`
}
type Status struct {
	Installed   bool     `json:"installed"`
	LoggedIn    bool     `json:"loggedIn"`
	Version     string   `json:"version,omitempty"`
	Account     string   `json:"account,omitempty"`
	Models      []Model  `json:"models"`
	Permissions []string `json:"permissions"`
	Error       string   `json:"error,omitempty"`
}

// Subscription is a lossless queue. The reader never blocks on an approval UI,
// so control replies and cancellation continue even during a pending question.
type Subscription struct {
	mu    sync.Mutex
	queue []Raw
	wake  chan struct{}
}

func newSubscription() *Subscription { return &Subscription{wake: make(chan struct{}, 1)} }
func (s *Subscription) push(r Raw) {
	s.mu.Lock()
	s.queue = append(s.queue, r)
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
func (s *Subscription) Wake() <-chan struct{} { return s.wake }
func (s *Subscription) Peek() (Raw, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queue) == 0 {
		return nil, false
	}
	return s.queue[0], true
}
func (s *Subscription) Next() (Raw, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queue) == 0 {
		return nil, false
	}
	r := s.queue[0]
	s.queue[0] = nil
	s.queue = s.queue[1:]
	return r, true
}

type Client struct {
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	writeMu   sync.Mutex
	mu        sync.Mutex
	pending   map[string]chan Raw
	requests  map[string]context.CancelFunc
	next      atomic.Uint64
	sub       *Subscription
	done      chan struct{}
	stop      context.CancelFunc
	closeOnce sync.Once
	err       error
	stderr    strings.Builder
	Info      Raw
	// Control is called concurrently for CLI requests; it must respect ctx.
	Control func(context.Context, Raw) (any, error)
}

func Start(ctx context.Context, opts Options, control func(context.Context, Raw) (any, error)) (*Client, error) {
	bin, err := FindBinary()
	if err != nil {
		return nil, err
	}
	args := []string{"-p", "--verbose", "--input-format", "stream-json", "--output-format", "stream-json", "--include-partial-messages", "--replay-user-messages", "--forward-subagent-text", "--include-hook-events", "--permission-prompt-tool", "stdio"}
	if opts.PermissionMode != "" {
		args = append(args, "--permission-mode", opts.PermissionMode)
	}
	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}
	if opts.Effort != "" {
		args = append(args, "--effort", opts.Effort)
	}
	if opts.Resume != "" {
		args = append(args, "--resume="+opts.Resume)
	} else if opts.SessionID != "" {
		args = append(args, "--session-id="+opts.SessionID)
	}
	if opts.SystemPrompt != "" {
		args = append(args, "--append-system-prompt", opts.SystemPrompt)
	}
	if opts.MaxBudget > 0 {
		args = append(args, "--max-budget-usd", fmt.Sprint(opts.MaxBudget))
	}
	if opts.ForkSession {
		args = append(args, "--fork-session")
	}
	if opts.ResumeAt != "" {
		args = append(args, "--resume-session-at="+opts.ResumeAt)
	}
	if opts.Cwd != "" {
		st, err := os.Stat(opts.Cwd)
		if err != nil {
			return nil, fmt.Errorf("claude workspace %q is unavailable: %w", opts.Cwd, err)
		}
		if !st.IsDir() {
			return nil, fmt.Errorf("claude workspace %q is not a directory", opts.Cwd)
		}
	}
	cmd := exec.Command(bin, args...)
	cmd.Dir = opts.Cwd
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "CLAUDECODE=") {
			cmd.Env = append(cmd.Env, e)
		}
	}
	cmd.Env = append(cmd.Env, "CLAUDE_CODE_SDK_READS_SESSION_STATE=1")
	prepareProcess(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, err
	}
	// Wait must not close stdout before the last result has been drained.
	lifetime, stop := context.WithCancel(context.Background())
	c := &Client{cmd: cmd, stdin: stdin, pending: map[string]chan Raw{}, requests: map[string]context.CancelFunc{}, sub: newSubscription(), done: make(chan struct{}), stop: stop, Control: control}
	cmd.Stderr = writerFunc(func(b []byte) (int, error) {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.stderr.Len() < 8192 {
			c.stderr.Write(b[:min(len(b), 8192-c.stderr.Len())])
		}
		return len(b), nil
	})
	cmd.WaitDelay = 2 * time.Second // a grandchild holding stderr must not hide the exit
	if err = cmd.Start(); err != nil {
		stop()
		_ = stdin.Close()
		return nil, err
	}
	go c.read(lifetime, stdout)
	var info Raw
	info, err = c.Call(ctx, map[string]any{"subtype": "initialize", "hooks": opts.Hooks})
	if err != nil {
		c.Close()
		return nil, fmt.Errorf("initialize Claude Code: %w", err)
	}
	if len(opts.Hooks) > 0 && (!info.Bool("hook_on_failure_supported") || info["hooks_applied"] != nil && !info.Bool("hooks_applied")) {
		c.Close()
		return nil, errors.New("claude code must support fail-closed host hooks; update the CLI before using it in Paladin")
	}
	c.Info = info
	return c, nil
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(b []byte) (int, error) { return f(b) }
func (c *Client) Done() <-chan struct{}          { return c.done }
func (c *Client) Subscription() *Subscription    { return c.sub }
func (c *Client) Err() error                     { c.mu.Lock(); defer c.mu.Unlock(); return c.err }
func (c *Client) Send(v any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return c.send(ctx, v)
}

func (c *Client) send(ctx context.Context, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	// A blocked pipe write must not pin shutdown behind the writer lock.
	// Timing out a write retires the transport: a partial JSON frame cannot
	// safely be followed by another request.
	// A context that is already done must not retire the process: AfterFunc
	// would run at once, and a turn cancelled before its first write would
	// kill the CLI a later turn reuses.
	if err := ctx.Err(); err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { _ = c.stdin.Close(); terminateProcess(c.cmd) })
	defer stop()
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-c.done:
		return ErrClosed
	default:
	}
	_, err = c.stdin.Write(append(b, '\n'))
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}
func (c *Client) Prompt(content any, id string) error {
	return c.Send(map[string]any{"type": "user", "uuid": id, "parent_tool_use_id": nil, "message": map[string]any{"role": "user", "content": content}})
}
func (c *Client) Call(ctx context.Context, request any) (Raw, error) {
	id := fmt.Sprintf("paladin-%d", c.next.Add(1))
	ch := make(chan Raw, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.pending, id); c.mu.Unlock() }()
	if err := c.send(ctx, map[string]any{"type": "control_request", "request_id": id, "request": request}); err != nil {
		return nil, err
	}
	select {
	case r := <-ch:
		if r.Str("subtype") == "error" {
			return nil, errors.New(r.Str("error"))
		}
		return r.Obj("response"), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.done:
		if err := c.Err(); err != nil {
			return nil, err
		}
		return nil, ErrClosed
	}
}
func (c *Client) read(ctx context.Context, stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), maxFrame)
	for scanner.Scan() {
		b := scanner.Bytes()
		if len(b) == 0 {
			continue
		}
		if b[0] != '{' {
			continue
		} // Some CLI builds emit SandboxDebug diagnostics.
		if !json.Valid(b) {
			c.mu.Lock()
			c.err = errors.New("invalid JSON from Claude Code")
			c.mu.Unlock()
			break
		}
		r := Decode(b)
		switch r.Str("type") {
		case "control_response":
			response := r.Obj("response")
			c.mu.Lock()
			ch := c.pending[response.Str("request_id")]
			c.mu.Unlock()
			if ch != nil {
				select {
				case ch <- response:
				default:
				}
			}
		case "control_request":
			id := r.Str("request_id")
			callCtx, cancel := context.WithCancel(ctx)
			c.mu.Lock()
			c.requests[id] = cancel
			c.mu.Unlock()
			go func() {
				defer cancel()
				defer func() { c.mu.Lock(); delete(c.requests, id); c.mu.Unlock() }()
				var v any
				var err error
				if c.Control == nil {
					err = errors.New("no active Claude permission host")
				} else {
					v, err = c.Control(callCtx, r.Obj("request"))
				}
				if callCtx.Err() != nil {
					return
				}
				response := map[string]any{"subtype": "success", "request_id": id, "response": v}
				if err != nil {
					response = map[string]any{"subtype": "error", "request_id": id, "error": err.Error()}
				}
				_ = c.Send(map[string]any{"type": "control_response", "response": response})
			}()
		case "control_cancel_request":
			c.mu.Lock()
			cancel := c.requests[r.Str("request_id")]
			c.mu.Unlock()
			if cancel != nil {
				cancel()
			}
		default:
			c.sub.push(r)
		}
	}
	scanErr := scanner.Err()
	c.stop()
	if scanErr != nil || c.Err() != nil {
		killProcess(c.cmd)
	}
	err := c.cmd.Wait()
	c.mu.Lock()
	if c.err == nil {
		if scanErr != nil {
			c.err = scanErr
		} else if err != nil {
			c.err = fmt.Errorf("claude Code exited: %w (%s)", err, strings.TrimSpace(c.stderr.String()))
		}
	}
	c.mu.Unlock()
	killProcess(c.cmd) // Clean up children left in the owned process group.
	close(c.done)
}
func (c *Client) Close() {
	c.closeOnce.Do(func() {
		c.stop()
		// Closing the pipe unblocks an in-flight writer; never wait for its lock.
		_ = c.stdin.Close()
		select {
		case <-c.done:
			return
		case <-time.After(2 * time.Second):
		}
		c.stop()
		terminateProcess(c.cmd)
		select {
		case <-c.done:
			return
		case <-time.After(3 * time.Second):
		}
		killProcess(c.cmd)
		<-c.done
	})
}
func (c *Client) Models() []Model {
	out := []Model{}
	seen := map[string]bool{}
	for _, m := range c.Info.List("models") {
		id := m.Str("resolvedModel")
		if id == "" {
			id = m.Str("value")
		}
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, Model{ID: id, DisplayName: m.Str("displayName"), Description: m.Str("description"), Efforts: m.Strings("supportedEffortLevels"), IsDefault: m.Str("value") == "default"})
	}
	return out
}
