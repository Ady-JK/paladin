//go:build unix

package tools

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func orphanCtx(t *testing.T, session string) *ToolCtx {
	t.Helper()
	return &ToolCtx{
		Workspace:  t.TempDir(),
		SessionDir: t.TempDir(),
		Cancel:     context.Background(),
		SessionID:  session,
	}
}

func shellResponse(t *testing.T, content string) ShellResponse {
	t.Helper()
	var res ShellResponse
	if err := json.Unmarshal([]byte(content), &res); err != nil {
		t.Fatalf("shell response %q: %v", content, err)
	}
	return res
}

func parsePID(t *testing.T, text string) int {
	t.Helper()
	pid, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil || pid <= 0 {
		t.Fatalf("not a pid: %q", text)
	}
	return pid
}

// waitReaped fails when the process is still signalable after a grace.
func waitReaped(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			return // ESRCH: gone (and reaped)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("process %d outlived the command that started it", pid)
}

func TestForegroundShellKillsProcessesItLeavesBehind(t *testing.T) {
	out, err := NewShellTool().Call(json.RawMessage(`{"command":"sleep 30 >/dev/null 2>&1 & echo $!"}`), orphanCtx(t, "orphan-fg"))
	if err != nil {
		t.Fatal(err)
	}
	res := shellResponse(t, out)
	if res.Output == nil {
		t.Fatalf("no output: %+v", res)
	}
	waitReaped(t, parsePID(t, *res.Output))
}

func TestBackgroundJobKillsProcessesItLeavesBehind(t *testing.T) {
	out, err := NewShellTool().Call(
		json.RawMessage(`{"command":"sleep 30 >/dev/null 2>&1 & echo $!","background":true}`),
		orphanCtx(t, "orphan-bg"),
	)
	if err != nil {
		t.Fatal(err)
	}
	res := shellResponse(t, out)
	if res.Log == nil {
		t.Fatalf("no log: %+v", res)
	}
	var pid int
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(*res.Log)
		if err == nil && strings.TrimSpace(string(data)) != "" {
			pid = parsePID(t, string(data))
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatalf("job log %s never recorded a pid", *res.Log)
	}
	waitReaped(t, pid)
}
