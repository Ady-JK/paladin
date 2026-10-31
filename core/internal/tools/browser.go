package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Ady-JK/paladin/core/internal/jsonx"
	"github.com/Ady-JK/paladin/core/internal/media"
)

const browserTimeout = 45 * time.Second

// BrowserRequest is emitted by agent-host to the Electron main process.
// One browser belongs to one session; the renderer is never entrusted with
// the model's automation request.
type BrowserRequest struct {
	Event     string `json:"event"`
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	Action    string `json:"action"`
	URL       string `json:"url,omitempty"`
	Selector  string `json:"selector,omitempty"`
	Role      string `json:"role,omitempty"`
	Name      string `json:"name,omitempty"`
	Text      string `json:"text,omitempty"`
	Key       string `json:"key,omitempty"`
	Value     string `json:"value,omitempty"`
	FullPage  bool   `json:"fullPage,omitempty"`
}

type BrowserReply struct {
	ID          string          `json:"id"`
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result,omitempty"`
	ImageBase64 string          `json:"imageBase64,omitempty"`
	Error       string          `json:"error,omitempty"`
}

type BrowserTool struct {
	mu      sync.Mutex
	sink    func(BrowserRequest)
	pending map[string]chan BrowserReply
	next    atomic.Uint64
}

func NewBrowserTool() *BrowserTool { return &BrowserTool{pending: make(map[string]chan BrowserReply)} }
func (*BrowserTool) Name() string  { return "browser" }

func (*BrowserTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "browser",
		Description: "Control this session's browser pane with Playwright. Only http://localhost and http://127.0.0.1 URLs are allowed. Open a local app, inspect its accessible snapshot, interact with the page, or capture a screenshot. Prefer role and accessible name from a snapshot; if a locator matches several elements, use a narrower Playwright selector. Clicks and other actions may be repeated when page state changes. Wait requires a locator, not a duration. Screenshots are sent visually to the model and saved in the session; the result includes a path that read can open later.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action":    map[string]any{"type": "string", "description": "Open a local URL, inspect or interact with the current page, navigate, or close the pane.", "enum": []string{"open", "snapshot", "click", "fill", "press", "hover", "select", "wait", "screenshot", "back", "forward", "reload", "close"}},
				"url":       map[string]any{"type": "string", "description": "Local URL for open."},
				"selector":  map[string]any{"type": "string", "description": "Playwright selector for an element; required for click, fill, press, hover, select or wait when role is omitted."},
				"role":      map[string]any{"type": "string", "description": "Accessible role from snapshot, used with name instead of selector. If supplied, role takes precedence over selector."},
				"name":      map[string]any{"type": "string", "description": "Accessible name for role."},
				"text":      map[string]any{"type": "string", "description": "Text to fill into an input."},
				"key":       map[string]any{"type": "string", "description": "Key to press, such as Enter or Control+A."},
				"value":     map[string]any{"type": "string", "description": "Option value for select."},
				"full_page": map[string]any{"type": "boolean", "description": "For screenshot, capture the full scrollable page."},
			},
			"required": []string{"action"},
		},
	}
}

func validateBrowserURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Scheme != "http" || u.Opaque != "" || (u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1") {
		return fmt.Errorf("browser: only http://localhost and http://127.0.0.1 URLs are allowed")
	}
	if u.Port() == "" && strings.Contains(u.Host, ":") {
		return fmt.Errorf("browser: invalid port")
	}
	return nil
}

func (t *BrowserTool) SetSink(fn func(BrowserRequest)) {
	t.mu.Lock()
	t.sink = fn
	t.mu.Unlock()
}

func (t *BrowserTool) Reply(reply BrowserReply) bool {
	t.mu.Lock()
	ch := t.pending[reply.ID]
	t.mu.Unlock()
	if ch == nil {
		return false
	}
	select {
	case ch <- reply:
		return true
	default:
		return false
	}
}

func (t *BrowserTool) Call(raw json.RawMessage, ctx *ToolCtx) (string, error) {
	out, err := t.CallRich(raw, ctx)
	return out.Content, err
}

func (t *BrowserTool) CallRich(raw json.RawMessage, ctx *ToolCtx) (RichOutput, error) {
	if ctx == nil || ctx.SessionID == "" {
		return RichOutput{}, ArgsError("browser: session is required")
	}
	var a struct {
		Action   string `json:"action"`
		URL      string `json:"url"`
		Selector string `json:"selector"`
		Role     string `json:"role"`
		Name     string `json:"name"`
		Text     string `json:"text"`
		Key      string `json:"key"`
		Value    string `json:"value"`
		FullPage bool   `json:"full_page"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return RichOutput{}, ArgsError("browser: invalid arguments")
	}
	switch a.Action {
	case "open":
		if err := validateBrowserURL(a.URL); err != nil {
			return RichOutput{}, ArgsError(err.Error())
		}
	case "click", "fill", "press", "hover", "select", "wait":
		if a.Selector == "" && a.Role == "" {
			return RichOutput{}, ArgsError("browser: selector or role is required")
		}
	case "snapshot", "screenshot", "back", "forward", "reload", "close":
	default:
		return RichOutput{}, ArgsError("browser: unknown action")
	}
	t.mu.Lock()
	sink := t.sink
	if sink == nil {
		t.mu.Unlock()
		return RichOutput{}, ArgsError("browser: the desktop browser is unavailable")
	}
	if len(t.pending) >= 64 {
		t.mu.Unlock()
		return RichOutput{}, ArgsError("browser: too many pending requests")
	}
	id := fmt.Sprintf("browser-%d", t.next.Add(1))
	ch := make(chan BrowserReply, 1)
	t.pending[id] = ch
	t.mu.Unlock()
	defer func() { t.mu.Lock(); delete(t.pending, id); t.mu.Unlock() }()
	req := BrowserRequest{Event: "browser/request", ID: id, SessionID: ctx.SessionID, Action: a.Action,
		URL: a.URL, Selector: a.Selector, Role: a.Role, Name: a.Name, Text: a.Text, Key: a.Key,
		Value: a.Value, FullPage: a.FullPage}
	sink(req)
	cancel := ctx.Cancel
	if cancel == nil {
		cancel = context.Background()
	}
	timer := time.NewTimer(browserTimeout)
	defer timer.Stop()
	var reply BrowserReply
	select {
	case reply = <-ch:
	case <-cancel.Done():
		return RichOutput{}, cancel.Err()
	case <-timer.C:
		return RichOutput{}, ArgsError("browser: timed out waiting for the desktop browser")
	}
	if !reply.OK {
		return RichOutput{}, ArgsError("browser: " + reply.Error)
	}
	if len(reply.Result) == 0 {
		reply.Result = json.RawMessage(`{}`)
	}
	if a.Action != "screenshot" {
		return RichOutput{Content: string(reply.Result)}, nil
	}
	if len(reply.ImageBase64) > (media.MaxBytes+2)/3*4+8 {
		return RichOutput{}, ArgsError("browser: screenshot exceeds image limit")
	}
	data, err := base64.StdEncoding.DecodeString(reply.ImageBase64)
	if err != nil {
		return RichOutput{}, ArgsError("browser: invalid screenshot data")
	}
	if media.MIME(data) == "" {
		return RichOutput{}, ArgsError("browser: unsupported screenshot format")
	}
	if ctx.SessionDir == "" {
		return RichOutput{}, ArgsError("browser: session directory is unavailable")
	}
	dir := filepath.Join(ctx.SessionDir, "browser", "screenshots")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return RichOutput{}, err
	}
	ext := ".png"
	if media.MIME(data) == "image/jpeg" {
		ext = ".jpg"
	}
	f, err := os.CreateTemp(dir, "shot-*"+ext)
	if err != nil {
		return RichOutput{}, err
	}
	path := f.Name()
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(path)
		return RichOutput{}, err
	}
	ref, err := media.Read(path)
	if err != nil {
		_ = os.Remove(path)
		return RichOutput{}, err
	}
	content := string(jsonx.MustMarshal(map[string]any{"path": path, "mime_type": ref.MIMEType, "size": ref.Size, "result": reply.Result}))
	return RichOutput{Content: content, Image: ref}, nil
}
