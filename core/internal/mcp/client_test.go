package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Ady-JK/paladin/core/internal/mcp/mcptest"
)

func stdioConfig(t *testing.T, args ...string) ServerConfig {
	return ServerConfig{Command: mcptest.Dummy(t), Args: args}
}

func connect(t *testing.T, cfg ServerConfig, opts Options) *Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c, err := Connect(ctx, "dummy", cfg, opts)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func call(t *testing.T, c *Client, name, args string) CallResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := c.CallTool(ctx, name, json.RawMessage(args))
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	return res
}

func toolNames(ts []Tool) string {
	var names []string
	for _, t := range ts {
		names = append(names, t.Name)
	}
	return strings.Join(names, ",")
}

func TestStdioListAndCall(t *testing.T) {
	c := connect(t, stdioConfig(t), Options{})
	if c.ServerInfo().Name != "mcp-dummy" || c.Instructions() == "" {
		t.Fatalf("server info %+v instructions %q", c.ServerInfo(), c.Instructions())
	}
	ctx := context.Background()
	tools, err := c.ListTools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := toolNames(tools); got != "echo,add,slow,fail,crash,image,grow,ping_client" {
		t.Fatalf("tools %s", got)
	}
	if !tools[0].Annotations.ReadOnly() || tools[2].Annotations.ReadOnly() || !tools[4].Annotations.Destructive() {
		t.Fatalf("annotations %+v %+v %+v", tools[0].Annotations, tools[2].Annotations, tools[4].Annotations)
	}
	if res := call(t, c, "echo", `{"text":"hi"}`); res.IsError || res.Text() != "hi" {
		t.Fatalf("echo %+v", res)
	}
	if res := call(t, c, "add", `{"a":2,"b":3}`); string(res.StructuredContent) != `{"sum":5}` {
		t.Fatalf("add %+v", res)
	}
	if res := call(t, c, "fail", `{}`); !res.IsError || res.Text() != "it broke" {
		t.Fatalf("fail %+v", res)
	}
	if res := call(t, c, "nope", `{}`); !res.IsError || !strings.Contains(res.Text(), "unknown tool") {
		t.Fatalf("protocol error should become an error result: %+v", res)
	}
}

func TestStdioPagination(t *testing.T) {
	c := connect(t, stdioConfig(t, "-page-size", "3"), Options{})
	tools, err := c.ListTools(context.Background())
	if err != nil || len(tools) != 8 {
		t.Fatalf("paged list: %d tools, %v", len(tools), err)
	}
}

func TestStdioCancelSendsNotification(t *testing.T) {
	c := connect(t, stdioConfig(t), Options{})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := c.CallTool(ctx, "slow", json.RawMessage(`{"ms":20000}`))
	if err == nil || time.Since(start) > 3*time.Second {
		t.Fatalf("cancel took %v err %v", time.Since(start), err)
	}
	// The connection survives a cancelled call.
	if res := call(t, c, "echo", `{"text":"still here"}`); res.Text() != "still here" {
		t.Fatalf("after cancel %+v", res)
	}
}

func TestStdioCrashIsReported(t *testing.T) {
	c := connect(t, stdioConfig(t), Options{})
	_, err := c.CallTool(context.Background(), "crash", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("crash call should fail")
	}
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Done not closed after the server exited")
	}
	if c.Err() == nil || !strings.Contains(c.Stderr(), "crashing on request") {
		t.Fatalf("err %v stderr %q", c.Err(), c.Stderr())
	}
	if _, err := c.CallTool(context.Background(), "echo", json.RawMessage(`{"text":"x"}`)); err == nil {
		t.Fatal("call on a dead connection should fail fast")
	}
}

func TestStdioStartupTimeoutKillsServer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	c, err := Connect(ctx, "dummy", stdioConfig(t, "-hang-init"), Options{})
	if err == nil {
		_ = c.Close()
		t.Fatal("hung initialize should fail")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("startup timeout took %v", time.Since(start))
	}
}

func TestStdioBadCommand(t *testing.T) {
	_, err := Connect(context.Background(), "x", ServerConfig{Command: "paladin-no-such-binary-xyz"}, Options{})
	if err == nil || !strings.Contains(err.Error(), "paladin-no-such-binary-xyz") {
		t.Fatalf("err %v", err)
	}
}

func TestStdioAnswersServerPing(t *testing.T) {
	c := connect(t, stdioConfig(t), Options{})
	if res := call(t, c, "ping_client", `{}`); res.IsError || res.Text() != "client answered" {
		t.Fatalf("ping_client %+v", res)
	}
}

func TestStdioToolsChangedNotification(t *testing.T) {
	var changed atomic.Int32
	c := connect(t, stdioConfig(t), Options{OnToolsChanged: func() { changed.Add(1) }})
	call(t, c, "grow", `{}`)
	waitUntil(func() bool { return changed.Load() > 0 })
	if changed.Load() == 0 {
		t.Fatal("list_changed not delivered")
	}
	tools, _ := c.ListTools(context.Background())
	if !strings.HasSuffix(toolNames(tools), ",extra") {
		t.Fatalf("tools after grow %s", toolNames(tools))
	}
}

func TestStdioCloseKillsChild(t *testing.T) {
	c := connect(t, stdioConfig(t), Options{})
	pid := c.PID()
	if pid <= 0 {
		t.Fatalf("pid %d", pid)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not finish the connection")
	}
	if processAlive(pid) {
		t.Fatalf("child %d still alive after Close", pid)
	}
}

func TestStdioEnvAndCwd(t *testing.T) {
	dir := t.TempDir()
	cfg := ServerConfig{Command: "sh", Args: []string{"-c", `printf '%s|%s\n' "$PWD" "$MCP_X" >&2; exit 1`},
		Env: map[string]string{"MCP_X": "from-env"}, Cwd: dir}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	_, err := Connect(context.Background(), "env", cfg, Options{})
	if err == nil || !strings.Contains(err.Error(), "from-env") {
		t.Fatalf("startup failure should carry the stderr tail: %v", err)
	}
}

func startHTTPDummy(t *testing.T, args ...string) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	cmd := exec.Command(mcptest.Dummy(t), append([]string{"-http", addr}, args...)...)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if conn, err := net.Dial("tcp", addr); err == nil {
			_ = conn.Close()
			return fmt.Sprintf("http://%s/mcp", addr)
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatal("http dummy never listened")
	return ""
}

func TestHTTPListAndCall(t *testing.T) {
	for _, sse := range []bool{false, true} {
		var args []string
		if sse {
			args = []string{"-sse"}
		}
		url := startHTTPDummy(t, args...)
		var changed atomic.Int32
		c := connect(t, ServerConfig{URL: url, Headers: map[string]string{"Authorization": "Bearer dummy-token"}},
			Options{OnToolsChanged: func() { changed.Add(1) }})
		tools, err := c.ListTools(context.Background())
		if err != nil || len(tools) != 8 {
			t.Fatalf("sse=%v list %d %v", sse, len(tools), err)
		}
		if res := call(t, c, "echo", `{"text":"over http"}`); res.Text() != "over http" {
			t.Fatalf("sse=%v echo %+v", sse, res)
		}
		call(t, c, "grow", `{}`)
		waitUntil(func() bool { return changed.Load() > 0 })
		if changed.Load() == 0 {
			t.Fatalf("sse=%v list_changed on the reply stream not delivered", sse)
		}
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestHTTPUnauthorized(t *testing.T) {
	url := startHTTPDummy(t)
	_, err := Connect(context.Background(), "h", ServerConfig{URL: url, Headers: map[string]string{"Authorization": "Bearer wrong"}}, Options{})
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("err %v", err)
	}
}

func TestHTTPSessionExpiryMarksDead(t *testing.T) {
	url := startHTTPDummy(t)
	c := connect(t, ServerConfig{URL: url}, Options{})
	// Drop the server-side session behind the client's back.
	req, _ := http.NewRequest(http.MethodDelete, url, nil)
	req.Header.Set("Mcp-Session-Id", c.tr.(*httpTransport).sessionID())
	if resp, err := http.DefaultClient.Do(req); err == nil {
		_ = resp.Body.Close()
	}
	if _, err := c.CallTool(context.Background(), "echo", json.RawMessage(`{"text":"x"}`)); err == nil {
		t.Fatal("expired session should fail the call")
	}
	select {
	case <-c.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("expired session should end the connection so the manager reconnects")
	}
}

func waitUntil(cond func() bool) {
	deadline := time.Now().Add(3 * time.Second)
	for !cond() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
}

func (t *httpTransport) sessionID() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.session
}
