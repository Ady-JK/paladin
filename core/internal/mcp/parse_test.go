package mcp

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func parseOne(t *testing.T, text string) NamedServer {
	t.Helper()
	list, err := ParseSnippet(text)
	if err != nil {
		t.Fatalf("%q: %v", text, err)
	}
	if len(list) != 1 {
		t.Fatalf("%q: want one server, got %+v", text, list)
	}
	return list[0]
}

func TestParseSnippetCommands(t *testing.T) {
	cases := []struct {
		text, name, command string
		args                []string
		env                 map[string]string
	}{
		{"npx -y @modelcontextprotocol/server-filesystem /Users/me/projects", "filesystem", "npx",
			[]string{"-y", "@modelcontextprotocol/server-filesystem", "/Users/me/projects"}, nil},
		{"uvx mcp-server-git --repository .", "git", "uvx", []string{"mcp-server-git", "--repository", "."}, nil},
		{"uvx --from git+https://github.com/x/y mcp-server-fetch", "fetch", "uvx",
			[]string{"--from", "git+https://github.com/x/y", "mcp-server-fetch"}, nil},
		{"GITHUB_TOKEN=abc npx -y @modelcontextprotocol/server-github", "github", "npx",
			[]string{"-y", "@modelcontextprotocol/server-github"}, map[string]string{"GITHUB_TOKEN": "abc"}},
		{"docker run -i --rm -e TOKEN mcp/github:latest", "github", "docker",
			[]string{"run", "-i", "--rm", "-e", "TOKEN", "mcp/github:latest"}, nil},
		{"npx -y @upstash/context7-mcp@latest", "context7", "npx", []string{"-y", "@upstash/context7-mcp@latest"}, nil},
		{"node /srv/tools/weather-server.js --port 0", "weather", "node", []string{"/srv/tools/weather-server.js", "--port", "0"}, nil},
		{`npx -y pkg-mcp "/path with space" 'single q'`, "pkg", "npx", []string{"-y", "pkg-mcp", "/path with space", "single q"}, nil},
		{"npx -y \\\n  @scope/server-thing \\\n  --flag", "thing", "npx", []string{"-y", "@scope/server-thing", "--flag"}, nil},
		{"/opt/bin/my-tool", "my-tool", "/opt/bin/my-tool", nil, nil},
		{"$ npx -y some-mcp-server", "some", "npx", []string{"-y", "some-mcp-server"}, nil},
	}
	for _, c := range cases {
		got := parseOne(t, c.text)
		if got.Name != c.name || got.Config.Command != c.command || !reflect.DeepEqual(got.Config.Args, c.args) || !reflect.DeepEqual(got.Config.Env, c.env) {
			t.Errorf("%q:\n got %+v", c.text, got)
		}
	}
}

func TestParseSnippetURLs(t *testing.T) {
	got := parseOne(t, "https://mcp.linear.app/mcp")
	if got.Name != "linear" || got.Config.Type != "http" || got.Config.URL != "https://mcp.linear.app/mcp" {
		t.Fatalf("%+v", got)
	}
	if got := parseOne(t, "http://127.0.0.1:8931/mcp"); got.Name != "mcp" || got.Config.URL == "" {
		t.Fatalf("%+v", got)
	}
}

func TestParseSnippetAgentCLIs(t *testing.T) {
	got := parseOne(t, "claude mcp add github -s user -e GITHUB_TOKEN=x -- npx -y @modelcontextprotocol/server-github")
	if got.Name != "github" || got.Config.Command != "npx" || got.Config.Env["GITHUB_TOKEN"] != "x" ||
		!reflect.DeepEqual(got.Config.Args, []string{"-y", "@modelcontextprotocol/server-github"}) {
		t.Fatalf("%+v", got)
	}
	got = parseOne(t, "claude mcp add --transport http linear https://mcp.linear.app/mcp --header \"Authorization: Bearer t\"")
	if got.Name != "linear" || got.Config.Type != "http" || got.Config.URL != "https://mcp.linear.app/mcp" || got.Config.Headers["Authorization"] != "Bearer t" {
		t.Fatalf("%+v", got)
	}
	got = parseOne(t, "codex mcp add docs --env A=1 -- npx -y docs-mcp")
	if got.Name != "docs" || got.Config.Command != "npx" || got.Config.Env["A"] != "1" {
		t.Fatalf("%+v", got)
	}
	if _, err := ParseSnippet("claude mcp add --transport sse x https://x/sse"); err == nil || !strings.Contains(err.Error(), "SSE") {
		t.Fatalf("sse: %v", err)
	}
}

func TestParseSnippetJSON(t *testing.T) {
	list, err := ParseSnippet(`{"mcpServers":{"b":{"command":"npx","args":["x"]},"a":{"url":"https://a/mcp"}}}`)
	if err != nil || len(list) != 2 || list[0].Name != "a" || list[1].Name != "b" || list[1].Config.Command != "npx" {
		t.Fatalf("%+v %v", list, err)
	}
	if list, err := ParseSnippet(`{"servers":{"fs":{"type":"stdio","command":"npx"}}}`); err != nil || len(list) != 1 || list[0].Name != "fs" {
		t.Fatalf("vs code shape: %+v %v", list, err)
	}
	if got := parseOne(t, `"fs": {"command": "npx", "args": ["-y", "@modelcontextprotocol/server-filesystem"]}`); got.Name != "fs" {
		t.Fatalf("bare entry: %+v", got)
	}
	if got := parseOne(t, `{"command":"uvx","args":["mcp-server-time"]}`); got.Name != "time" || got.Config.Command != "uvx" {
		t.Fatalf("single config: %+v", got)
	}
	if _, err := ParseSnippet(`{"mcpServers":{"x":{"type":"sse","url":"http://x"}}}`); err == nil || !strings.Contains(err.Error(), "SSE") {
		t.Fatalf("sse json: %v", err)
	}
	if _, err := ParseSnippet(`{"mcpServers":{`); err == nil {
		t.Fatal("broken json")
	}
}

func TestParseSnippetSeveralLinesAndErrors(t *testing.T) {
	list, err := ParseSnippet("npx -y @a/server-git\n\n# comment\nuvx mcp-server-git\n")
	if err != nil || len(list) != 2 || list[0].Name != "git" || list[1].Name != "git-2" {
		t.Fatalf("%+v %v", list, err)
	}
	for _, bad := range []string{"", "   \n ", `npx "unterminated`, "FOO=bar"} {
		if _, err := ParseSnippet(bad); err == nil {
			t.Errorf("%q should fail", bad)
		}
	}
}

func TestManagerAddServers(t *testing.T) {
	m, dir, _ := newTestManager(t, nil)
	add := []NamedServer{{Name: "dummy", Config: dummyCfg(t)}}
	if err := m.AddServers(ScopeGlobal, "", add, false); err != nil {
		t.Fatal(err)
	}
	if err := m.AddServers(ScopeGlobal, "", add, false); err == nil || !strings.Contains(err.Error(), "already") {
		t.Fatalf("duplicate: %v", err)
	}
	if err := m.AddServers(ScopeGlobal, "", add, true); err != nil {
		t.Fatalf("replace: %v", err)
	}
	if err := m.AddServers(ScopeGlobal, "", []NamedServer{{Name: "x", Config: ServerConfig{Type: "sse", URL: "http://x"}}}, false); err == nil {
		t.Fatal("invalid config must be refused")
	}
	if err := m.AddServers(ScopeGlobal, "", nil, false); err == nil {
		t.Fatal("nothing to add")
	}
	waitUntil(func() bool { return statusOf(m, "", ScopeGlobal, "dummy").State == StateReady })

	// Other keys and servers in the file survive.
	ws := t.TempDir()
	path := FolderPath(ws)
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, []byte(`{"mcpServers":{"keep":{"command":"x"}},"note":"hi"}`), 0o644)
	if err := m.Trust(ws, true); err != nil {
		t.Fatal(err)
	}
	if err := m.AddServers(ScopeFolder, ws, []NamedServer{{Name: "new", Config: ServerConfig{URL: "https://n/mcp"}}}, false); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	for _, want := range []string{`"keep"`, `"note": "hi"`, `"new"`, `"https://n/mcp"`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("missing %s in %s", want, data)
		}
	}
	if !m.Trusted(ws) {
		t.Fatal("adding to a trusted folder config keeps it trusted")
	}
	_ = dir
}

func TestManagerAddServersToUntrustedFolderStaysUntrusted(t *testing.T) {
	m, _, _ := newTestManager(t, nil)
	ws := t.TempDir()
	path := FolderPath(ws)
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, []byte(`{"mcpServers":{"sneaky":{"command":"x"}}}`), 0o644)
	if err := m.AddServers(ScopeFolder, ws, []NamedServer{{Name: "mine", Config: ServerConfig{URL: "https://n/mcp"}}}, false); err != nil {
		t.Fatal(err)
	}
	if m.Trusted(ws) {
		t.Fatal("adding a server must not trust servers someone else put there")
	}
	// A brand-new folder config is the user's own and is trusted.
	ws2 := t.TempDir()
	if err := m.AddServers(ScopeFolder, ws2, []NamedServer{{Name: "mine", Config: ServerConfig{URL: "https://n/mcp"}}}, false); err != nil {
		t.Fatal(err)
	}
	if !m.Trusted(ws2) {
		t.Fatal("a new folder config written by the user is trusted")
	}
}
