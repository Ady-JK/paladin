package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestMain doubles as a fake language server: with PALADIN_FAKE_LSP=1
// the test binary speaks LSP on stdio. Every line containing ERROR is
// published as an error and WARN as a warning; FAKE_SILENT never
// publishes and FAKE_INIT_FAIL fails initialize. FAKE_LOG names a file
// that gets "pid N" at start and "doc URI VERSION FIRSTLINE" for every
// didOpen and didChange.
func TestMain(m *testing.M) {
	if os.Getenv("PALADIN_FAKE_LSP") == "1" {
		fakeServer()
		return
	}
	os.Exit(m.Run())
}

func fakeServer() {
	r := bufio.NewReader(os.Stdin)
	logf := func(string, ...any) {}
	if path := os.Getenv("FAKE_LOG"); path != "" {
		if f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			logf = func(format string, args ...any) { fmt.Fprintf(f, format+"\n", args...) }
		}
	}
	logf("pid %d", os.Getpid())
	write := func(v any) {
		body, _ := json.Marshal(v)
		fmt.Fprintf(os.Stdout, "Content-Length: %d\r\n\r\n%s", len(body), body)
	}
	for {
		body, err := readFrame(r)
		if err != nil {
			os.Exit(0)
		}
		var m message
		_ = json.Unmarshal(body, &m)
		switch m.Method {
		case "initialize":
			if os.Getenv("FAKE_INIT_FAIL") == "1" {
				write(map[string]any{"jsonrpc": "2.0", "id": m.ID, "error": map[string]any{"code": -32603, "message": "no project"}})
				continue
			}
			// Ask the client something first, as real servers do.
			write(map[string]any{"jsonrpc": "2.0", "id": 99, "method": "workspace/configuration",
				"params": map[string]any{"items": []any{map[string]any{"section": "fake"}}}})
			write(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": map[string]any{"capabilities": map[string]any{}}})
		case "shutdown":
			write(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": nil})
		case "exit":
			os.Exit(0)
		case "textDocument/didOpen", "textDocument/didChange":
			var p struct {
				TextDocument struct {
					URI     string `json:"uri"`
					Version int    `json:"version"`
					Text    string `json:"text"`
				} `json:"textDocument"`
				ContentChanges []struct {
					Text string `json:"text"`
				} `json:"contentChanges"`
			}
			_ = json.Unmarshal(m.Params, &p)
			text := p.TextDocument.Text
			if len(p.ContentChanges) > 0 {
				text = p.ContentChanges[0].Text
			}
			first, _, _ := strings.Cut(text, "\n")
			logf("doc %s %d %s", p.TextDocument.URI, p.TextDocument.Version, first)
			if os.Getenv("FAKE_SILENT") == "1" {
				continue
			}
			var diags []map[string]any
			for i, line := range strings.Split(text, "\n") {
				sev := 0
				switch {
				case strings.Contains(line, "ERROR"):
					sev = 1
				case strings.Contains(line, "WARN"):
					sev = 2
				}
				if sev == 0 {
					continue
				}
				col := strings.Index(line, "ERROR")
				if col < 0 {
					col = 0
				}
				diags = append(diags, map[string]any{"severity": sev, "message": "bad: " + strings.TrimSpace(line),
					"range": map[string]any{"start": map[string]int{"line": i, "character": col}, "end": map[string]int{"line": i, "character": col + 1}}})
			}
			if diags == nil {
				diags = []map[string]any{}
			}
			write(map[string]any{"jsonrpc": "2.0", "method": "textDocument/publishDiagnostics",
				"params": map[string]any{"uri": p.TextDocument.URI, "version": p.TextDocument.Version, "diagnostics": diags}})
		}
	}
}

// fakeManager is a manager whose only server is the fake, for *.fk.
func fakeManager(t *testing.T, env ...string) (*Manager, string) {
	t.Helper()
	data := t.TempDir()
	cfg := map[string]any{
		"timeoutMs": 2000,
		"servers": map[string]any{
			"fake": map[string]any{"command": []string{os.Args[0]}, "extensions": []string{".fk"},
				"languageId": "fake", "env": append([]string{"PALADIN_FAKE_LSP=1"}, env...)},
		},
	}
	raw, _ := json.Marshal(cfg)
	if err := os.MkdirAll(filepath.Join(data, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ConfigPath(data), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	m := NewManager(data)
	t.Cleanup(m.Close)
	return m, t.TempDir()
}

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDiagnoseReportsErrorsAndFollowsChanges(t *testing.T) {
	m, ws := fakeManager(t)
	path := filepath.Join(ws, "pkg", "a.fk")
	writeFile(t, path, "ok\n  x ERROR here\nWARN only\nERROR again\n")
	res := m.Diagnose(context.Background(), ws, path, nil)
	if res.Server != "fake" || res.Timedout || res.Errors != 2 || len(res.Diagnostics) != 2 {
		t.Fatalf("got %+v", res)
	}
	first := res.Diagnostics[0]
	if first.Line != 2 || first.Column != 5 || !strings.Contains(first.Message, "x ERROR here") || first.Source != "fake" {
		t.Fatalf("1-based position and message: %+v", first)
	}
	// The same document changes: the new version's result wins.
	writeFile(t, path, "all good\n")
	if res := m.Diagnose(context.Background(), ws, path, nil); res.Errors != 0 || res.Timedout {
		t.Fatalf("after the fix: %+v", res)
	}
	st := m.Status()
	if len(st) != 1 || st[0].ID != "fake" || st[0].State != "ready" || st[0].PID == 0 {
		t.Fatalf("status %+v", st)
	}
}

func TestDiagnoseSkipsWhatItCannotCheck(t *testing.T) {
	m, ws := fakeManager(t)
	outside := filepath.Join(t.TempDir(), "o.fk")
	writeFile(t, outside, "ERROR\n")
	if res := m.Diagnose(context.Background(), ws, outside, nil); res.Server != "" {
		t.Fatalf("outside the workspace: %+v", res)
	}
	other := filepath.Join(ws, "notes.unknownext")
	writeFile(t, other, "ERROR\n")
	if res := m.Diagnose(context.Background(), ws, other, nil); res.Server != "" {
		t.Fatalf("no server for the extension: %+v", res)
	}
}

func TestASilentServerTimesOutQuickly(t *testing.T) {
	m, ws := fakeManager(t, "FAKE_SILENT=1")
	raw, _ := os.ReadFile(ConfigPath(m.dataDir))
	os.WriteFile(ConfigPath(m.dataDir), []byte(strings.Replace(string(raw), `"timeoutMs":2000`, `"timeoutMs":200`, 1)), 0o644)
	path := filepath.Join(ws, "a.fk")
	writeFile(t, path, "ERROR\n")
	start := time.Now()
	res := m.Diagnose(context.Background(), ws, path, nil)
	if !res.Timedout || res.Errors != 0 {
		t.Fatalf("got %+v", res)
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("waited %v for a silent server", took)
	}
}

func TestAFailingServerIsMarkedAndSkipped(t *testing.T) {
	m, ws := fakeManager(t, "FAKE_INIT_FAIL=1")
	path := filepath.Join(ws, "a.fk")
	writeFile(t, path, "ERROR\n")
	if res := m.Diagnose(context.Background(), ws, path, nil); res.Errors != 0 {
		t.Fatalf("got %+v", res)
	}
	st := m.Status()
	if len(st) != 1 || st[0].State != "failed" || !strings.Contains(st[0].Error, "no project") {
		t.Fatalf("status %+v", st)
	}
	start := time.Now()
	m.Diagnose(context.Background(), ws, path, nil)
	if time.Since(start) > 500*time.Millisecond {
		t.Fatal("a broken server must not be retried on every edit")
	}
}

func TestIdleAndClosedServersStop(t *testing.T) {
	m, ws := fakeManager(t)
	path := filepath.Join(ws, "a.fk")
	writeFile(t, path, "ok\n")
	m.Diagnose(context.Background(), ws, path, nil)
	st := m.Status()
	if len(st) != 1 || st[0].PID == 0 {
		t.Fatalf("status %+v", st)
	}
	pid := st[0].PID
	m.idle = 0
	m.stopIdle()
	if len(m.Status()) != 0 {
		t.Fatal("an idle server is stopped")
	}
	if syscall.Kill(pid, 0) == nil {
		t.Fatalf("process %d still running", pid)
	}
	m.idle = time.Hour
	m.Diagnose(context.Background(), ws, path, nil)
	pid = m.Status()[0].PID
	m.Close()
	if syscall.Kill(pid, 0) == nil {
		t.Fatalf("process %d survived Close", pid)
	}
}

func TestWarmStartsAndOpensWithoutWaiting(t *testing.T) {
	m, ws := fakeManager(t)
	path := filepath.Join(ws, "a.fk")
	writeFile(t, path, "ERROR\n")
	m.Warm(ws, path, nil)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if st := m.Status(); len(st) == 1 && st[0].State == "ready" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("warm did not start the server: %+v", m.Status())
}

func TestReadFrameRejectsBadHeaders(t *testing.T) {
	for _, in := range []string{"Content-Length: x\r\n\r\n{}", "\r\n{}", fmt.Sprintf("Content-Length: %d\r\n\r\n", maxMessage+1)} {
		if _, err := readFrame(bufio.NewReader(strings.NewReader(in))); err == nil {
			t.Errorf("%q: expected an error", in)
		}
	}
}

func TestLsJSONOverridesBuiltins(t *testing.T) {
	data := t.TempDir()
	os.MkdirAll(filepath.Join(data, "config"), 0o755)
	os.WriteFile(ConfigPath(data), []byte(`{"servers":{"gopls":{"disabled":true},"pyright":{"command":["/opt/pyright","--stdio"]}}}`), 0o644)
	specs, _, err := loadSpecs(data)
	if err != nil {
		t.Fatal(err)
	}
	if !specs["gopls"].Disabled || specs["pyright"].Command[0] != "/opt/pyright" || specs["pyright"].Extensions[0] != ".py" {
		t.Fatalf("specs %+v %+v", specs["gopls"], specs["pyright"])
	}
	if got := candidates(specs, "/x/a.go"); len(got) != 0 {
		t.Fatalf("disabled gopls still picked: %+v", got)
	}
}
