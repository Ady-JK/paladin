package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseFileValidates(t *testing.T) {
	f, err := ParseFile([]byte(`{"mcpServers":{"fs":{"command":"npx","args":["-y","x"]},"web":{"type":"http","url":"https://x/mcp"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if f.McpServers["fs"].Transport() != TransportStdio || f.McpServers["web"].Transport() != TransportHTTP {
		t.Fatalf("%+v", f)
	}
	for _, bad := range []string{
		`{"mcpServers":{"x":{}}}`,
		`{"mcpServers":{"x":{"type":"sse","url":"https://x/sse"}}}`,
		`{"mcpServers":{"":{"command":"a"}}}`,
		`{"mcpServers":{"x":{"command":"a","toolTimeoutSec":-1}}}`,
		`not json`,
	} {
		if _, err := ParseFile([]byte(bad)); err == nil {
			t.Errorf("%s should fail", bad)
		}
	}
	if f, err := ParseFile([]byte(" ")); err != nil || len(f.McpServers) != 0 {
		t.Fatalf("empty file: %v %v", f, err)
	}
	_, err = ParseFile([]byte(`{"mcpServers":{"x":{"type":"sse","url":"https://x/sse"}}}`))
	if err == nil || !strings.Contains(err.Error(), "Streamable HTTP") {
		t.Fatalf("sse hint: %v", err)
	}
}

func TestExpandConfig(t *testing.T) {
	t.Setenv("MCP_TOKEN", "s3cret")
	folder := t.TempDir()
	cfg := ServerConfig{
		Command: "${MCP_HOME:-/opt}/bin/srv",
		Args:    []string{"--root", "${workspaceFolder}", "$literal"},
		Env:     map[string]string{"TOKEN": "${MCP_TOKEN}"},
		Cwd:     "sub",
		URL:     "https://h/${MCP_MISSING}x",
		Headers: map[string]string{"Authorization": "Bearer ${MCP_TOKEN}"},
	}
	got := expand(cfg, folder)
	if got.Command != "/opt/bin/srv" || got.Args[1] != folder || got.Args[2] != "$literal" {
		t.Fatalf("%+v", got)
	}
	if got.Env["TOKEN"] != "s3cret" || got.Headers["Authorization"] != "Bearer s3cret" || got.URL != "https://h/x" {
		t.Fatalf("%+v", got)
	}
	if got.Cwd != filepath.Join(folder, "sub") {
		t.Fatalf("cwd %q", got.Cwd)
	}
	if cfg.Env["TOKEN"] != "${MCP_TOKEN}" {
		t.Fatal("expand must not mutate the input maps")
	}
	if g := expand(ServerConfig{Command: "x"}, folder); g.Cwd != folder {
		t.Fatalf("folder servers default to the folder: %q", g.Cwd)
	}
}

func TestTrustFollowsContent(t *testing.T) {
	dir := t.TempDir()
	ts := newTrustStore(filepath.Join(dir, "trust.json"))
	folder := "/work/repo"
	fp := Fingerprint([]byte(`{"mcpServers":{}}`))
	if ts.Trusted(folder, fp) {
		t.Fatal("new folder must not be trusted")
	}
	if err := ts.Set(folder, fp); err != nil {
		t.Fatal(err)
	}
	if !newTrustStore(filepath.Join(dir, "trust.json")).Trusted(folder, fp) {
		t.Fatal("trust should persist")
	}
	if ts.Trusted(folder, Fingerprint([]byte(`{"mcpServers":{"x":{"command":"evil"}}}`))) {
		t.Fatal("changed config must be re-approved")
	}
	_ = ts.Set(folder, "")
	if ts.Trusted(folder, fp) {
		t.Fatal("revoked")
	}
}

func TestWriteFileIsAtomicAndCreatesDirs(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a", ".paladin", "mcp.json")
	if err := WriteFile(p, []byte(`{"mcpServers":{}}`)); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "{\"mcpServers\":{}}\n" {
		t.Fatalf("%q", b)
	}
}

func TestExposedNames(t *testing.T) {
	if got := exposedName("github", "create_issue"); got != "mcp__github__create_issue" {
		t.Fatal(got)
	}
	if got := exposedName("my server!", "do.thing"); got != "mcp__my_server___do_thing" {
		t.Fatal(got)
	}
	long := exposedName("a-very-long-server-name-for-testing", strings.Repeat("tool", 20))
	if len(long) > maxToolName || !strings.HasPrefix(long, "mcp__a-very-long") {
		t.Fatalf("%d %s", len(long), long)
	}
	if exposedName("s", strings.Repeat("x", 80)+"1") == exposedName("s", strings.Repeat("x", 80)+"2") {
		t.Fatal("truncated names must stay distinct")
	}
}

func TestSanitizeSchema(t *testing.T) {
	cases := map[string]string{
		``:                `{"properties":{},"type":"object"}`,
		`null`:            `{"properties":{},"type":"object"}`,
		`{"$schema":"x"}`: `{"properties":{},"type":"object"}`,
		`{"type":"object","properties":{"a":{"type":"string"}},"required":["a"]}`: `{"properties":{"a":{"type":"string"}},"required":["a"],"type":"object"}`,
		`[1]`: `{"properties":{},"type":"object"}`,
	}
	for in, want := range cases {
		got, _ := json.Marshal(sanitizeSchema(json.RawMessage(in)))
		if string(got) != want {
			t.Errorf("%s -> %s want %s", in, got, want)
		}
	}
}

func TestRenderResult(t *testing.T) {
	res := CallResult{Content: []Content{
		{Type: "text", Text: "hello"},
		{Type: "image", MimeType: "image/png", Data: "iVBORw0KGgo="},
		{Type: "resource_link", URI: "file:///a.txt", Name: "a.txt"},
		{Type: "resource", Resource: &ResourceContents{URI: "mem://n", Text: "note body"}},
		{Type: "audio", MimeType: "audio/wav", Data: "AAAA"},
	}}
	got := renderResult(res)
	for _, want := range []string{"hello", "[image image/png, 8 bytes]", "[resource link a.txt: file:///a.txt]", "[resource mem://n]\nnote body", "[audio audio/wav, 3 bytes]"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
	if got := renderResult(CallResult{StructuredContent: json.RawMessage(`{"sum":5}`)}); got != `{"sum":5}` {
		t.Fatalf("structured only: %q", got)
	}
	if got := renderResult(CallResult{}); got != "(no output)" {
		t.Fatalf("empty: %q", got)
	}
	big := renderResult(CallResult{Content: []Content{{Type: "text", Text: strings.Repeat("a", maxResultChars*2)}}})
	if len(big) > maxResultChars+200 || !strings.Contains(big, "characters omitted") {
		t.Fatalf("not truncated: %d", len(big))
	}
}
