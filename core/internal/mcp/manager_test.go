package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Ady-JK/paladin/core/internal/mcp/mcptest"
	"github.com/Ady-JK/paladin/core/internal/tools"
)

type statusLog struct {
	mu     sync.Mutex
	events []ServerStatus
}

func (l *statusLog) add(s ServerStatus) {
	l.mu.Lock()
	l.events = append(l.events, s)
	l.mu.Unlock()
}

func (l *statusLog) states(name string) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for _, e := range l.events {
		if e.Name == name {
			out = append(out, e.State)
		}
	}
	return out
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, _ := json.Marshal(v)
	if err := WriteFile(path, data); err != nil {
		t.Fatal(err)
	}
}

func servers(m map[string]ServerConfig) File { return File{McpServers: m} }

func newTestManager(t *testing.T, global map[string]ServerConfig) (*Manager, string, *statusLog) {
	t.Helper()
	dataDir := t.TempDir()
	if global != nil {
		writeJSON(t, GlobalPath(dataDir), servers(global))
	}
	log := &statusLog{}
	m := NewManager(dataDir, log.add)
	t.Cleanup(m.Close)
	return m, dataDir, log
}

func dummyCfg(t *testing.T, args ...string) ServerConfig {
	return ServerConfig{Command: mcptest.Dummy(t), Args: args}
}

func toolsCtx() context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	_ = cancel
	return ctx
}

func exposed(ts []tools.Tool) []string {
	var out []string
	for _, t := range ts {
		out = append(out, t.Name())
	}
	sort.Strings(out)
	return out
}

func find(t *testing.T, ts []tools.Tool, name string) tools.Tool {
	t.Helper()
	for _, tool := range ts {
		if tool.Name() == name {
			return tool
		}
	}
	t.Fatalf("tool %s not in %v", name, exposed(ts))
	return nil
}

func runTool(t *testing.T, tool tools.Tool, args string) (string, error) {
	t.Helper()
	return tool.Call(json.RawMessage(args), &tools.ToolCtx{Cancel: context.Background()})
}

func statusOf(m *Manager, folder, scope, name string) ServerStatus {
	for _, s := range m.Status(folder) {
		if s.Name == name && s.Scope == scope {
			return s
		}
	}
	return ServerStatus{}
}

func TestManagerGlobalToolsAndStatus(t *testing.T) {
	m, _, log := newTestManager(t, map[string]ServerConfig{"dummy": dummyCfg(t)})
	ts := m.Tools(toolsCtx(), "")
	if len(ts) != 8 {
		t.Fatalf("tools %v", exposed(ts))
	}
	echo := find(t, ts, "mcp__dummy__echo")
	out, err := runTool(t, echo, `{"text":"hi"}`)
	if err != nil || out != "hi" {
		t.Fatalf("echo %q %v", out, err)
	}
	info := echo.(tools.MCPTool).MCPInfo()
	if info.Server != "dummy" || info.Tool != "echo" || !info.ReadOnly {
		t.Fatalf("info %+v", info)
	}
	if p, ok := echo.(tools.ParallelSafe); !ok || !p.ParallelSafe() {
		t.Fatal("read-only MCP tools may run in parallel")
	}
	if def := find(t, ts, "mcp__dummy__fail").Definition(); def.Parameters.(map[string]any)["type"] != "object" {
		t.Fatalf("schema not sanitized: %+v", def.Parameters)
	}
	if _, err := runTool(t, find(t, ts, "mcp__dummy__fail"), `{}`); err == nil || !strings.Contains(err.Error(), "it broke") {
		t.Fatalf("isError should fail the call: %v", err)
	}
	st := statusOf(m, "", ScopeGlobal, "dummy")
	if st.State != StateReady || len(st.Tools) != 8 || st.Server == nil || st.Server.Name != "mcp-dummy" || st.Transport != TransportStdio {
		t.Fatalf("status %+v", st)
	}
	if got := strings.Join(log.states("dummy"), ","); !strings.Contains(got, "starting,ready") {
		t.Fatalf("events %s", got)
	}
	if !strings.Contains(m.Instructions(""), "Dummy server for Paladin tests") {
		t.Fatalf("instructions %q", m.Instructions(""))
	}
}

func TestManagerAllowDenyLists(t *testing.T) {
	cfg := dummyCfg(t)
	cfg.AllowedTools = []string{"echo", "add"}
	cfg.DeniedTools = []string{"add"}
	m, _, _ := newTestManager(t, map[string]ServerConfig{"d": cfg})
	if got := exposed(m.Tools(toolsCtx(), "")); strings.Join(got, ",") != "mcp__d__echo" {
		t.Fatalf("%v", got)
	}
}

func TestManagerFolderServersNeedTrust(t *testing.T) {
	m, _, _ := newTestManager(t, nil)
	folder := t.TempDir()
	writeJSON(t, FolderPath(folder), servers(map[string]ServerConfig{"local": dummyCfg(t)}))
	if got := m.Tools(toolsCtx(), folder); len(got) != 0 {
		t.Fatalf("untrusted folder servers must not start: %v", exposed(got))
	}
	if st := statusOf(m, folder, ScopeFolder, "local"); st.State != StateUntrusted {
		t.Fatalf("status %+v", st)
	}
	if err := m.Trust(folder, true); err != nil {
		t.Fatal(err)
	}
	find(t, m.Tools(toolsCtx(), folder), "mcp__local__echo")
	if st := statusOf(m, folder, ScopeFolder, "local"); st.State != StateReady || st.Folder != folder {
		t.Fatalf("status %+v", st)
	}
	// An edit behind the app's back (git pull) needs approval again.
	cfg := dummyCfg(t, "-page-size", "2")
	writeJSON(t, FolderPath(folder), servers(map[string]ServerConfig{"local": cfg}))
	if got := m.Tools(toolsCtx(), folder); len(got) != 0 {
		t.Fatalf("changed config must be re-trusted: %v", exposed(got))
	}
	if st := statusOf(m, folder, ScopeFolder, "local"); st.State != StateUntrusted {
		t.Fatalf("status %+v", st)
	}
	// Other folders never see it.
	if got := m.Tools(toolsCtx(), t.TempDir()); len(got) != 0 {
		t.Fatalf("leaked into another folder: %v", exposed(got))
	}
}

func TestManagerFolderShadowsGlobal(t *testing.T) {
	m, _, _ := newTestManager(t, map[string]ServerConfig{"dummy": dummyCfg(t)})
	folder := t.TempDir()
	local := dummyCfg(t)
	local.AllowedTools = []string{"echo"}
	writeJSON(t, FolderPath(folder), servers(map[string]ServerConfig{"dummy": local}))
	if err := m.Trust(folder, true); err != nil {
		t.Fatal(err)
	}
	if got := exposed(m.Tools(toolsCtx(), folder)); strings.Join(got, ",") != "mcp__dummy__echo" {
		t.Fatalf("folder server should replace the global one: %v", got)
	}
	if st := statusOf(m, folder, ScopeGlobal, "dummy"); !st.Shadowed {
		t.Fatalf("global should be marked shadowed: %+v", st)
	}
	if got := m.Tools(toolsCtx(), ""); len(got) != 8 {
		t.Fatalf("global sessions keep the global server: %v", exposed(got))
	}
}

func TestManagerRestartsAfterCrash(t *testing.T) {
	m, _, _ := newTestManager(t, map[string]ServerConfig{"dummy": dummyCfg(t)})
	ts := m.Tools(toolsCtx(), "")
	if _, err := runTool(t, find(t, ts, "mcp__dummy__crash"), `{}`); err == nil {
		t.Fatal("crash call should fail")
	}
	waitUntil(func() bool { return statusOf(m, "", ScopeGlobal, "dummy").State == StateFailed })
	st := statusOf(m, "", ScopeGlobal, "dummy")
	if st.State != StateFailed || !strings.Contains(st.Stderr, "crashing on request") {
		t.Fatalf("status after crash %+v", st)
	}
	// The old wrapper reconnects on its next call.
	out, err := runTool(t, find(t, ts, "mcp__dummy__echo"), `{"text":"back"}`)
	if err != nil || out != "back" {
		t.Fatalf("after restart %q %v", out, err)
	}
	st = statusOf(m, "", ScopeGlobal, "dummy")
	if st.State != StateReady || st.Restarts != 1 {
		t.Fatalf("status %+v", st)
	}
	// The crash log outlives the restart so the user can still see why.
	crash, restarted := strings.Index(st.Stderr, "crashing on request"), strings.Index(st.Stderr, "restarted")
	if crash < 0 || restarted < crash {
		t.Fatalf("stderr after restart %q", st.Stderr)
	}
}

func TestManagerToolTimeout(t *testing.T) {
	cfg := dummyCfg(t)
	cfg.ToolTimeoutSec = 0.3
	m, _, _ := newTestManager(t, map[string]ServerConfig{"dummy": cfg})
	start := time.Now()
	_, err := runTool(t, find(t, m.Tools(toolsCtx(), ""), "mcp__dummy__slow"), `{"ms":10000}`)
	if err == nil || !strings.Contains(err.Error(), "timed out") || time.Since(start) > 3*time.Second {
		t.Fatalf("err %v after %v", err, time.Since(start))
	}
}

func TestManagerTurnCancelStopsCall(t *testing.T) {
	m, _, _ := newTestManager(t, map[string]ServerConfig{"dummy": dummyCfg(t)})
	slow := find(t, m.Tools(toolsCtx(), ""), "mcp__dummy__slow")
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)
	_, err := slow.Call(json.RawMessage(`{"ms":10000}`), &tools.ToolCtx{Cancel: ctx})
	if err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("err %v", err)
	}
}

func TestManagerStartupFailureIsNotRetriedEveryTurn(t *testing.T) {
	cfg := dummyCfg(t, "-hang-init")
	cfg.StartupTimeoutSec = 0.4
	m, _, _ := newTestManager(t, map[string]ServerConfig{"hang": cfg, "dummy": dummyCfg(t)})
	if got := m.Tools(toolsCtx(), ""); len(got) != 8 {
		t.Fatalf("healthy server tools should still load: %v", exposed(got))
	}
	st := statusOf(m, "", ScopeGlobal, "hang")
	if st.State != StateFailed || st.Error == "" {
		t.Fatalf("status %+v", st)
	}
	start := time.Now()
	m.Tools(toolsCtx(), "")
	if time.Since(start) > 200*time.Millisecond {
		t.Fatalf("a failed server should not block every turn (%v)", time.Since(start))
	}
}

func TestManagerEnableDisable(t *testing.T) {
	off := false
	cfg := dummyCfg(t)
	cfg.Enabled = &off
	m, dataDir, _ := newTestManager(t, map[string]ServerConfig{"dummy": cfg})
	if got := m.Tools(toolsCtx(), ""); len(got) != 0 {
		t.Fatalf("disabled: %v", exposed(got))
	}
	if st := statusOf(m, "", ScopeGlobal, "dummy"); st.State != StateDisabled {
		t.Fatalf("%+v", st)
	}
	if err := m.SetEnabled(ScopeGlobal, "", "dummy", true); err != nil {
		t.Fatal(err)
	}
	if got := m.Tools(toolsCtx(), ""); len(got) != 8 {
		t.Fatalf("enabled: %v", exposed(got))
	}
	if err := m.SetEnabled(ScopeGlobal, "", "dummy", false); err != nil {
		t.Fatal(err)
	}
	if st := statusOf(m, "", ScopeGlobal, "dummy"); st.State != StateDisabled {
		t.Fatalf("%+v", st)
	}
	data, _ := os.ReadFile(GlobalPath(dataDir))
	if !strings.Contains(string(data), `"enabled": false`) || !strings.Contains(string(data), `"command"`) {
		t.Fatalf("file %s", data)
	}
	if err := m.SetEnabled(ScopeGlobal, "", "nope", true); err == nil {
		t.Fatal("unknown server should fail")
	}
}

func TestManagerSetConfig(t *testing.T) {
	m, dataDir, _ := newTestManager(t, nil)
	if err := m.SetConfig(ScopeGlobal, "", `{"mcpServers":{"x":{}}}`); err == nil {
		t.Fatal("invalid config must be rejected")
	}
	if _, err := os.Stat(GlobalPath(dataDir)); err == nil {
		t.Fatal("rejected config must not be written")
	}
	text := fmt.Sprintf(`{"mcpServers":{"dummy":{"command":%q}}}`, mcptest.Dummy(t))
	if err := m.SetConfig(ScopeGlobal, "", text); err != nil {
		t.Fatal(err)
	}
	if got := m.Tools(toolsCtx(), ""); len(got) != 8 {
		t.Fatalf("%v", exposed(got))
	}
	cfg, err := m.Config(ScopeGlobal, "")
	if err != nil || cfg.Path != GlobalPath(dataDir) || !strings.Contains(cfg.Text, "dummy") {
		t.Fatalf("%+v %v", cfg, err)
	}
	// Writing a folder config in the app trusts what the user wrote.
	folder := t.TempDir()
	if err := m.SetConfig(ScopeFolder, folder, text); err != nil {
		t.Fatal(err)
	}
	if st := statusOf(m, folder, ScopeFolder, "dummy"); st.State == StateUntrusted {
		t.Fatalf("app-written folder config should be trusted: %+v", st)
	}
	// Removing a server stops it.
	if err := m.SetConfig(ScopeGlobal, "", `{"mcpServers":{}}`); err != nil {
		t.Fatal(err)
	}
	if st := statusOf(m, "", ScopeGlobal, "dummy"); st.Name != "" {
		t.Fatalf("removed server still listed: %+v", st)
	}
}

func TestManagerPicksUpListChanged(t *testing.T) {
	m, _, _ := newTestManager(t, map[string]ServerConfig{"dummy": dummyCfg(t)})
	if _, err := runTool(t, find(t, m.Tools(toolsCtx(), ""), "mcp__dummy__grow"), `{}`); err != nil {
		t.Fatal(err)
	}
	waitUntil(func() bool { return len(statusOf(m, "", ScopeGlobal, "dummy").Tools) == 9 })
	find(t, m.Tools(toolsCtx(), ""), "mcp__dummy__extra")
}

func TestManagerRestartAndClose(t *testing.T) {
	m, _, _ := newTestManager(t, map[string]ServerConfig{"dummy": dummyCfg(t)})
	m.Tools(toolsCtx(), "")
	pid := m.pid(ScopeGlobal, "", "dummy")
	if err := m.Restart(ScopeGlobal, "", "dummy"); err != nil {
		t.Fatal(err)
	}
	m.Tools(toolsCtx(), "")
	pid2 := m.pid(ScopeGlobal, "", "dummy")
	if pid2 == 0 || pid2 == pid || processAlive(pid) {
		t.Fatalf("restart pids %d -> %d", pid, pid2)
	}
	m.Close()
	waitUntil(func() bool { return !processAlive(pid2) })
	if processAlive(pid2) {
		t.Fatal("Close left the server running")
	}
	if got := m.Tools(toolsCtx(), ""); len(got) != 0 {
		t.Fatal("closed manager serves no tools")
	}
}

func TestManagerBrokenConfigIsReported(t *testing.T) {
	m, dataDir, _ := newTestManager(t, nil)
	if err := os.MkdirAll(filepath.Dir(GlobalPath(dataDir)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(GlobalPath(dataDir), []byte("{oops"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := m.Tools(toolsCtx(), ""); len(got) != 0 {
		t.Fatal("no tools from a broken file")
	}
	if errs := m.ConfigErrors(""); !strings.Contains(errs[ScopeGlobal], "invalid JSON") {
		t.Fatalf("errors %v", errs)
	}
}

func TestManagerCrashBudget(t *testing.T) {
	m, _, _ := newTestManager(t, map[string]ServerConfig{"dummy": dummyCfg(t)})
	ts := m.Tools(toolsCtx(), "")
	crash := find(t, ts, "mcp__dummy__crash")
	for i := 0; i < crashBudget; i++ {
		_, _ = runTool(t, crash, `{}`)
		waitUntil(func() bool { return statusOf(m, "", ScopeGlobal, "dummy").State == StateFailed })
	}
	_, err := runTool(t, find(t, ts, "mcp__dummy__echo"), `{"text":"x"}`)
	if err == nil || !strings.Contains(err.Error(), "crashed") {
		t.Fatalf("over budget should stop restarting: %v", err)
	}
	if err := m.Restart(ScopeGlobal, "", "dummy"); err != nil {
		t.Fatal(err)
	}
	if out, err := runTool(t, find(t, ts, "mcp__dummy__echo"), `{"text":"x"}`); err != nil || out != "x" {
		t.Fatalf("manual restart clears the budget: %q %v", out, err)
	}
}
