package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"sync"
	"testing"
)

func TestBrowserToolPerSessionAndScreenshot(t *testing.T) {
	browser := NewBrowserTool()
	var mu sync.Mutex
	var requests []BrowserRequest
	browser.SetSink(func(req BrowserRequest) {
		mu.Lock()
		requests = append(requests, req)
		mu.Unlock()
		go browser.Reply(BrowserReply{ID: req.ID, OK: true, Result: json.RawMessage(`{"title":"hello"}`), ImageBase64: base64.StdEncoding.EncodeToString([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0})})
	})
	ctx := &ToolCtx{SessionID: "session-a", SessionDir: t.TempDir(), Cancel: context.Background()}
	got, err := browser.CallRich(json.RawMessage(`{"action":"screenshot"}`), ctx)
	if err != nil || got.Image == nil || got.Image.MIMEType != "image/png" {
		t.Fatalf("screenshot: %+v, %v", got, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 1 || requests[0].SessionID != "session-a" {
		t.Fatalf("requests: %+v", requests)
	}
}

func TestBrowserToolRejectsExternalNavigation(t *testing.T) {
	browser := NewBrowserTool()
	browser.SetSink(func(BrowserRequest) { t.Fatal("external URL reached browser") })
	ctx := &ToolCtx{SessionID: "s", SessionDir: t.TempDir(), Cancel: context.Background()}
	for _, url := range []string{"http://example.com", "file:///tmp/x", "http://localhost.evil.test", "http://[::1]:8080"} {
		raw, _ := json.Marshal(map[string]string{"action": "open", "url": url})
		if _, err := browser.CallRich(raw, ctx); err == nil {
			t.Fatalf("accepted %s", url)
		}
	}
	if err := validateBrowserURL("http://127.0.0.1:8080"); err != nil {
		t.Fatal(err)
	}
	if err := validateBrowserURL("http://localhost:8080"); err != nil {
		t.Fatal(err)
	}
}
