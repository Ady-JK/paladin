package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func fetch(t *testing.T, ctx *ToolCtx, args map[string]any) (WebFetchResponse, error) {
	t.Helper()
	raw, _ := json.Marshal(args)
	out, err := WebFetchTool{}.Call(raw, ctx)
	var resp WebFetchResponse
	if err == nil {
		if jerr := json.Unmarshal([]byte(out), &resp); jerr != nil {
			t.Fatalf("result %s: %v", out, jerr)
		}
	}
	return resp, err
}

// noProxy keeps tests off any proxy in the environment.
func noProxy(t *testing.T) {
	t.Helper()
	old := webProxy
	webProxy = func(*http.Request) (*url.URL, error) { return nil, nil }
	t.Cleanup(func() { webProxy = old })
}

// fakeDNS answers lookups from a table.
func fakeDNS(t *testing.T, table map[string]string) {
	t.Helper()
	old := webLookup
	webLookup = func(_ context.Context, host string) ([]net.IP, error) {
		if ip, ok := table[host]; ok {
			return []net.IP{net.ParseIP(ip)}, nil
		}
		return nil, fmt.Errorf("no such host")
	}
	t.Cleanup(func() { webLookup = old })
}

const samplePage = `<!doctype html><html><head><title>Pricing — Acme</title><style>.x{}</style>
<script>alert("no")</script></head><body><nav><a href="/">Home</a> <a href="/blog">Blog</a></nav>
<main><h1>Plans</h1><p>Choose a <strong>plan</strong> that fits.
See <a href="/docs/limits">the limits</a>.</p>
<ul><li>Free: 1 seat</li><li>Team: <em>10</em> seats</li></ul>
<table><tr><th>Plan</th><th>Price</th></tr><tr><td>Free</td><td>$0</td></tr><tr><td>Team</td><td>$9 | user</td></tr></table>
<pre><code>curl https://acme.test/api</code></pre>
<blockquote>Fast and simple.</blockquote></main><footer>© Acme</footer></body></html>`

func TestWebFetchReturnsReadableMarkdown(t *testing.T) {
	noProxy(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("no user agent sent")
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, samplePage)
	}))
	defer srv.Close()
	resp, err := fetch(t, nil, map[string]any{"url": srv.URL + "/pricing"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# Plans", "Choose a **plan** that fits.", "[the limits](" + srv.URL + "/docs/limits)",
		"- Free: 1 seat", "- Team: _10_ seats", "| Plan | Price |", "| --- | --- |", `| Team | $9 \| user |`,
		"```\ncurl https://acme.test/api\n```", "> Fast and simple."} {
		if !strings.Contains(resp.Content, want) {
			t.Errorf("missing %q in:\n%s", want, resp.Content)
		}
	}
	for _, unwanted := range []string{"alert", ".x{}", "Home", "© Acme"} {
		if strings.Contains(resp.Content, unwanted) {
			t.Errorf("page chrome or script %q kept:\n%s", unwanted, resp.Content)
		}
	}
	if resp.Title != "Pricing — Acme" || resp.Status != 200 || resp.ContentType != "text/html" || !strings.Contains(resp.Note, "untrusted") {
		t.Fatalf("response %+v", resp)
	}
	plain, err := fetch(t, nil, map[string]any{"url": srv.URL, "format": "text"})
	if err != nil || strings.Contains(plain.Content, "**") || !strings.Contains(plain.Content, "Choose a plan that fits.") {
		t.Fatalf("plain text: %v %q", err, plain.Content)
	}
	raw, err := fetch(t, nil, map[string]any{"url": srv.URL, "format": "html"})
	if err != nil || !strings.Contains(raw.Content, "<script>") {
		t.Fatalf("raw html: %v", err)
	}
}

func TestWebFetchPagesThroughLongContent(t *testing.T) {
	noProxy(t)
	body := strings.Repeat("abcdefghij", 50)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, body)
	}))
	defer srv.Close()
	first, err := fetch(t, nil, map[string]any{"url": srv.URL, "max_chars": 200})
	if err != nil || len(first.Content) != 200 || !first.Truncated || first.NextIndex != 200 || first.TotalChars != 500 {
		t.Fatalf("first chunk %v %+v", err, first)
	}
	last, err := fetch(t, nil, map[string]any{"url": srv.URL, "max_chars": 200, "start_index": 400})
	if err != nil || len(last.Content) != 100 || last.Truncated || last.NextIndex != 0 {
		t.Fatalf("last chunk %v %+v", err, last)
	}
	capped, err := fetch(t, &ToolCtx{Web: WebOptions{MaxBytes: 50}}, map[string]any{"url": srv.URL})
	if err != nil || len(capped.Content) != 50 || !capped.Truncated || !strings.Contains(capped.Note, "first 50 bytes") {
		t.Fatalf("byte cap %v %+v", err, capped)
	}
}

func TestWebFetchRefusesUnsafeTargets(t *testing.T) {
	noProxy(t)
	fakeDNS(t, map[string]string{"rebind.test": "127.0.0.1", "intranet.test": "10.1.2.3", "meta.test": "169.254.169.254"})
	for _, c := range []struct{ url, want string }{
		{"ftp://example.com/x", "http:// or https://"},
		{"file:///etc/passwd", "http:// or https://"},
		{"https://user:pw@example.com/", "user name or password"},
		{"/relative", "http:// or https://"},
		{"http://10.0.0.1/", "private network"},
		{"http://192.168.1.1/", "private network"},
		{"http://169.254.169.254/latest/meta-data", "link-local"},
		{"http://rebind.test/", "only localhost and 127.0.0.1"},
		{"http://intranet.test/", "private network"},
		{"http://meta.test/", "link-local"},
	} {
		_, err := fetch(t, nil, map[string]any{"url": c.url})
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want %q, got %v", c.url, c.want, err)
		}
	}
}

func TestWebFetchAllowsPrivateOnlyWhenTheUserDoes(t *testing.T) {
	noProxy(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "internal docs") }))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	// The name resolves to loopback, standing in for an intranet host.
	fakeDNS(t, map[string]string{"wiki.corp.test": "127.0.0.1"})
	target := "http://wiki.corp.test:" + port + "/"
	if _, err := fetch(t, nil, map[string]any{"url": target}); err == nil {
		t.Fatal("blocked by default")
	}
	resp, err := fetch(t, &ToolCtx{Web: WebOptions{AllowPrivate: true}}, map[string]any{"url": target})
	if err != nil || resp.Content != "internal docs" {
		t.Fatalf("allowed: %v %+v", err, resp)
	}
}

func TestWebFetchFollowsOnlySameSiteRedirects(t *testing.T) {
	noProxy(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/old":
			http.Redirect(w, r, "/new", http.StatusMovedPermanently)
		case "/away":
			http.Redirect(w, r, "https://elsewhere.example/page", http.StatusFound)
		case "/missing":
			http.NotFound(w, r)
		case "/image":
			w.Header().Set("Content-Type", "image/png")
			w.Write([]byte{0x89, 'P', 'N', 'G'})
		default:
			fmt.Fprint(w, "new page")
		}
	}))
	defer srv.Close()
	resp, err := fetch(t, nil, map[string]any{"url": srv.URL + "/old"})
	if err != nil || resp.Content != "new page" || resp.FinalURL != srv.URL+"/new" {
		t.Fatalf("same site: %v %+v", err, resp)
	}
	resp, err = fetch(t, nil, map[string]any{"url": srv.URL + "/away"})
	if err != nil || resp.RedirectTo != "https://elsewhere.example/page" || resp.Content != "" {
		t.Fatalf("other site: %v %+v", err, resp)
	}
	if _, err := fetch(t, nil, map[string]any{"url": srv.URL + "/missing"}); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("http error: %v", err)
	}
	if _, err := fetch(t, nil, map[string]any{"url": srv.URL + "/image"}); err == nil || !strings.Contains(err.Error(), "not text") {
		t.Fatalf("binary: %v", err)
	}
}

func TestSameSite(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"example.com", "example.com", true}, {"docs.example.com", "example.com", true},
		{"example.com", "www.example.com", true}, {"badexample.com", "example.com", false},
		{"example.com.evil.test", "example.com", false}, {"a.example.com", "b.example.com", false},
	} {
		if got := SameSite(c.a, c.b); got != c.want {
			t.Errorf("SameSite(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

func TestWebFetchSaysWhenAPageNeedsJavaScript(t *testing.T) {
	noProxy(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><head><title>App</title></head><body><div id="root"></div><script>render()</script></body></html>`)
	}))
	defer srv.Close()
	resp, err := fetch(t, nil, map[string]any{"url": srv.URL})
	if err != nil || resp.Content != "" || resp.Title != "App" || !strings.Contains(resp.Note, "browser tool") {
		t.Fatalf("%v %+v", err, resp)
	}
}
