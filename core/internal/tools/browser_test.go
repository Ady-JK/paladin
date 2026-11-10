package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
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

// The browser opens web sites (the gate asks per site first) but never
// files, credentials in URLs, or private addresses unless allowed.
func TestBrowserToolOpensOnlySafeURLs(t *testing.T) {
	noProxy(t)
	fakeDNS(t, map[string]string{"intranet.test": "10.2.3.4", "example.com": "93.184.216.34"})
	browser := NewBrowserTool()
	browser.SetSink(func(BrowserRequest) { t.Fatal("a refused URL reached the browser") })
	ctx := &ToolCtx{SessionID: "s", SessionDir: t.TempDir(), Cancel: context.Background()}
	for _, url := range []string{"file:///tmp/x", "https://user:pw@example.com", "http://10.0.0.1", "http://intranet.test/", "javascript:alert(1)"} {
		raw, _ := json.Marshal(map[string]string{"action": "open", "url": url})
		if _, err := browser.CallRich(raw, ctx); err == nil {
			t.Fatalf("accepted %s", url)
		}
	}
	for _, url := range []string{"http://127.0.0.1:8080", "http://localhost:8080", "https://example.com/docs"} {
		if err := validateBrowserURL(url, ctx); err != nil {
			t.Fatalf("%s: %v", url, err)
		}
	}
	allowed := *ctx
	allowed.Web.AllowPrivate = true
	if err := validateBrowserURL("http://intranet.test/", &allowed); err != nil {
		t.Fatalf("private allowed: %v", err)
	}
	// The pane connects only to addresses resolved and checked on this
	// machine, so a name that does not resolve here is refused up front.
	for _, c := range []*ToolCtx{ctx, &allowed} {
		if err := validateBrowserURL("https://proxy-only.test/", c); err == nil || !strings.Contains(err.Error(), "cannot resolve") {
			t.Fatalf("unresolvable name: %v", err)
		}
	}
}

// A key press with no locator targets the page itself, so page-level
// shortcuts work; the other interactions still need an element.
func TestBrowserPressWorksWithoutLocator(t *testing.T) {
	browser := NewBrowserTool()
	var got []BrowserRequest
	browser.SetSink(func(req BrowserRequest) {
		got = append(got, req)
		go browser.Reply(BrowserReply{ID: req.ID, OK: true})
	})
	ctx := &ToolCtx{SessionID: "s", SessionDir: t.TempDir(), Cancel: context.Background()}
	if _, err := browser.CallRich(json.RawMessage(`{"action":"press","key":"Control+Alt+Slash"}`), ctx); err != nil {
		t.Fatalf("press without a locator: %v", err)
	}
	if len(got) != 1 || got[0].Action != "press" || got[0].Key != "Control+Alt+Slash" {
		t.Fatalf("request: %+v", got)
	}
	for _, action := range []string{"click", "fill", "hover", "select", "wait"} {
		raw, _ := json.Marshal(map[string]string{"action": action})
		if _, err := browser.CallRich(raw, ctx); err == nil || !strings.Contains(err.Error(), "selector or role is required") {
			t.Fatalf("%s without a locator: %v", action, err)
		}
	}
}

// Every request tells the desktop app whether private networks are
// allowed, so its proxy follows the user's switch.
func TestBrowserRequestsCarryThePrivateNetworkSwitch(t *testing.T) {
	browser := NewBrowserTool()
	var got []bool
	browser.SetSink(func(req BrowserRequest) {
		got = append(got, req.PrivateNetwork)
		go browser.Reply(BrowserReply{ID: req.ID, OK: true})
	})
	ctx := &ToolCtx{SessionID: "s", SessionDir: t.TempDir(), Cancel: context.Background()}
	if _, err := browser.CallRich(json.RawMessage(`{"action":"snapshot"}`), ctx); err != nil {
		t.Fatal(err)
	}
	ctx.Web.AllowPrivate = true
	if _, err := browser.CallRich(json.RawMessage(`{"action":"snapshot"}`), ctx); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] || !got[1] {
		t.Fatalf("privateNetwork sent: %v", got)
	}
	raw, _ := json.Marshal(BrowserRequest{Action: "snapshot"})
	if !strings.Contains(string(raw), `"privateNetwork":false`) {
		t.Fatalf("the switch must be sent even when off: %s", raw)
	}
}
