package permission

import (
	"testing"
)

func webReq(t *testing.T, tool string, a map[string]any) (Request, bool) {
	t.Helper()
	return Classify(tool, args(a), "/ws")
}

func TestWebCallsAreClassifiedBySite(t *testing.T) {
	r, gated := webReq(t, "web_fetch", map[string]any{"url": "https://Docs.Python.org/3/library/json.html"})
	if !gated || r.Kind != KindWeb || r.Host != "docs.python.org" || r.URL == "" {
		t.Fatalf("web_fetch: %v %+v", gated, r)
	}
	r, gated = webReq(t, "browser", map[string]any{"action": "open", "url": "https://chatgpt.com/"})
	if !gated || r.Kind != KindWeb || r.Host != "chatgpt.com" {
		t.Fatalf("browser open: %v %+v", gated, r)
	}
	for _, c := range []struct {
		tool string
		a    map[string]any
	}{
		{"web_fetch", map[string]any{"url": "not a url"}},
		{"browser", map[string]any{"action": "snapshot"}},
	} {
		if _, gated := webReq(t, c.tool, c.a); gated {
			t.Errorf("%s %v should not ask", c.tool, c.a)
		}
	}
}

func TestNewSitesAskAndRulesDecide(t *testing.T) {
	req, _ := webReq(t, "web_fetch", map[string]any{"url": "https://docs.example.com/a"})
	for _, p := range []Preset{ReadOnly, Auto} {
		d := Decide(p, req, nil)
		if d.Verdict != Ask || len(d.Remember) != 1 || d.Remember[0] != (Rule{Tool: ToolWeb, Pattern: "docs.example.com", Action: ActionAllow}) {
			t.Fatalf("%s: %+v", p, d)
		}
		if len(d.Choices) != 5 {
			t.Fatalf("site cards offer once/session/folder/everywhere/deny: %+v", d.Choices)
		}
	}
	if d := Decide(FullAccess, req, nil); d.Verdict != Allow {
		t.Fatalf("full access: %+v", d)
	}
	allowSite := []Rule{{Tool: ToolWeb, Pattern: "example.com", Action: ActionAllow}}
	if d := Decide(Auto, req, allowSite); d.Verdict != Allow {
		t.Fatalf("a site rule covers its subdomains: %+v", d)
	}
	other, _ := webReq(t, "web_fetch", map[string]any{"url": "https://badexample.com/"})
	if d := Decide(Auto, other, allowSite); d.Verdict != Ask {
		t.Fatalf("a look-alike domain is not covered: %+v", d)
	}
	deny := []Rule{{Tool: ToolWeb, Pattern: "*", Action: ActionDeny}}
	if d := Decide(FullAccess, req, deny); d.Verdict != Deny {
		t.Fatalf("deny wins over full access: %+v", d)
	}
}

func TestWebRulesValidate(t *testing.T) {
	for _, p := range []string{"example.com", "*", "*.example.com", "sub.example.co.uk", "localhost:5173"} {
		if err := (Rule{Tool: ToolWeb, Pattern: p, Action: ActionAllow}).validate(); err != nil {
			t.Errorf("%q: %v", p, err)
		}
	}
	for _, p := range []string{"https://example.com", "example.com/path", "exa mple.com", "*.", "user@example.com", "localhost:99999", "example.com:443"} {
		if err := (Rule{Tool: ToolWeb, Pattern: p, Action: ActionAllow}).validate(); err == nil {
			t.Errorf("%q should be rejected", p)
		}
	}
	if got := (Rule{Tool: ToolWeb, Pattern: " *.Example.COM ", Action: ActionAllow}).normalized().Pattern; got != "example.com" {
		t.Fatalf("normalized %q", got)
	}
}

// Services on this machine ask once per port, like a site: the LiteLLM
// proxy on :4000 is not reachable just because a dev server on :5173 is.
func TestLocalhostAsksPerPort(t *testing.T) {
	for _, c := range []struct{ tool, url, host string }{
		{"web_fetch", "http://localhost:3000/api", "localhost:3000"},
		{"web_fetch", "http://127.0.0.1:4000/v1/models", "localhost:4000"},
		{"web_fetch", "http://[::1]:8080/", "localhost:8080"},
		{"web_fetch", "http://localhost/", "localhost:80"},
		{"web_fetch", "https://localhost/", "localhost:443"},
		{"browser", "http://127.0.0.1:5173", "localhost:5173"},
	} {
		a := map[string]any{"url": c.url}
		if c.tool == "browser" {
			a["action"] = "open"
		}
		r, gated := webReq(t, c.tool, a)
		if !gated || r.Kind != KindWeb || r.Host != c.host {
			t.Fatalf("%s: %v %+v", c.url, gated, r)
		}
		d := Decide(Auto, r, nil)
		if d.Verdict != Ask || d.Remember[0].Pattern != c.host || len(d.Choices) != 5 {
			t.Fatalf("%s: %+v", c.url, d)
		}
	}
	dev, _ := webReq(t, "web_fetch", map[string]any{"url": "http://localhost:5173/"})
	proxy, _ := webReq(t, "web_fetch", map[string]any{"url": "http://127.0.0.1:4000/"})
	rules := []Rule{{Tool: ToolWeb, Pattern: "localhost:5173", Action: ActionAllow}}
	if d := Decide(Auto, dev, rules); d.Verdict != Allow {
		t.Fatalf("an approved port: %+v", d)
	}
	if d := Decide(Auto, proxy, rules); d.Verdict != Ask {
		t.Fatalf("another port: %+v", d)
	}
	// "Every site" is about the web, not this machine.
	if d := Decide(Auto, proxy, []Rule{{Tool: ToolWeb, Pattern: "*", Action: ActionAllow}}); d.Verdict != Ask {
		t.Fatalf("* covered localhost: %+v", d)
	}
	if d := Decide(Auto, proxy, []Rule{{Tool: ToolWeb, Pattern: "*", Action: ActionDeny}}); d.Verdict != Deny {
		t.Fatalf("a deny-all still covers localhost: %+v", d)
	}
}
