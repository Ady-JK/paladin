package agent

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/inference"
	"github.com/Ady-JK/paladin/core/internal/tools"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

// verifyAgent is a tool-capable agent with write registered and the
// check-before-finishing nudge on.
func verifyAgent(t *testing.T, prov *mockProvider) (*Agent, transcript.Session, string) {
	t.Helper()
	a, sess, _, done := testAgent(t, prov)
	t.Cleanup(func() { close(done) })
	a.tools.Register(tools.NewWriteTool(tools.NewEngine()))
	a.toolDefs = append(a.toolDefs, inference.ToolDefinition{Name: "write", Description: "w"})
	a.verify = true
	ws := t.TempDir()
	a.toolCtx.Workspace = ws
	return a, sess, ws
}

func writeArgs(path string) string {
	raw, _ := json.Marshal(map[string]any{"path": path, "content": "package x\n"})
	return string(raw)
}

func TestNudgesOnceWhenCodeChangedWithoutAnyRun(t *testing.T) {
	prov := &mockProvider{}
	a, sess, ws := verifyAgent(t, prov)
	file := filepath.Join(ws, "x.go")
	prov.streams = []streamScript{
		callRound("w1", "write", writeArgs(file)), textRound("done"),
		callRound("w2", "write", writeArgs(filepath.Join(ws, "y.go"))), textRound("still done"),
	}
	out, err := a.Run()
	if err != nil {
		t.Fatal(err)
	}
	if prov.calls != 4 {
		t.Fatalf("one nudge, then the turn may end: %d calls", prov.calls)
	}
	nudge := lastUser(prov.streamMsgs[2])
	if !strings.Contains(nudge, file) || !strings.Contains(nudge, "test") {
		t.Fatalf("the nudge names the changed files: %q", nudge)
	}
	if !strings.Contains(out.Reply, "still done") {
		t.Fatalf("reply %q", out.Reply)
	}
	h, _ := sess.History()
	found := false
	for _, m := range h {
		if m.Role == transcript.RoleUser && m.Display == verifyDisplay {
			found = true
		}
	}
	if !found {
		t.Fatalf("the nudge is kept in history with a short display: %+v", h)
	}
}

func TestNoNudgeAfterARunOrForDocsOrWhenOff(t *testing.T) {
	cases := []struct {
		name   string
		rounds func(ws string) []streamScript
		off    bool
	}{
		{"ran a check after the edit", func(ws string) []streamScript {
			return []streamScript{
				callRound("w", "write", writeArgs(filepath.Join(ws, "x.go"))),
				callRound("s", "shell", `{"cmd":"cd pkg && go test ./..."}`), textRound("done"),
			}
		}, false},
		{"only docs changed", func(ws string) []streamScript {
			return []streamScript{callRound("w", "write", writeArgs(filepath.Join(ws, "README.md"))), textRound("done")}
		}, false},
		{"the write failed", func(ws string) []streamScript {
			return []streamScript{callRound("w", "write", `{"path":"relative.go","content":"x"}`), textRound("done")}
		}, false},
		{"switched off", func(ws string) []streamScript {
			return []streamScript{callRound("w", "write", writeArgs(filepath.Join(ws, "x.go"))), textRound("done")}
		}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			prov := &mockProvider{}
			a, _, ws := verifyAgent(t, prov)
			a.verify = !c.off
			a.tools.Register(&sbProbe{}) // a shell that "passes" without running
			prov.streams = c.rounds(ws)
			if _, err := a.Run(); err != nil {
				t.Fatal(err)
			}
			if prov.calls != len(prov.streams) {
				t.Fatalf("no nudge expected: %d calls for %d rounds", prov.calls, len(prov.streams))
			}
		})
	}
}

// shellResult is a finished foreground shell call's result.
func shellResult(exit int, output string) *tools.ToolResult {
	raw, _ := json.Marshal(map[string]any{"status": "done", "exit_code": exit, "output": output})
	return &tools.ToolResult{Name: "shell", Content: string(raw), IsError: exit != 0}
}

func shellCall(cmd string) *inference.ToolCall {
	raw, _ := json.Marshal(map[string]string{"cmd": cmd})
	return &inference.ToolCall{Name: "shell", Arguments: string(raw)}
}

// Only a real check that passed clears the reminder: unrelated or
// refused commands do not, and a failed check turns it into "the last
// check failed".
func TestOnlyAPassingCheckClearsTheReminder(t *testing.T) {
	cases := []struct {
		name, cmd string
		result    *tools.ToolResult
		cleared   bool
		failed    bool
	}{
		{"pwd", "pwd", shellResult(0, "/ws"), false, false},
		{"ls", "ls -la", shellResult(0, "x.go"), false, false},
		{"a version probe", "go version", shellResult(0, "go1.27"), false, false},
		{"refused", "go test ./...", &tools.ToolResult{Name: "shell", IsError: true, Content: `{"error":"denied by the user"}`}, false, false},
		{"background", "go test ./...", &tools.ToolResult{Name: "shell", Content: `{"job_id":"j1","status":"running"}`}, false, false},
		{"go test", "go test ./...", shellResult(0, "ok  x"), true, false},
		{"build in a folder", "cd app && npm run build", shellResult(0, "built"), true, false},
		{"python compile", "python3 -m py_compile x.py", shellResult(0, ""), true, false},
		{"make target", "make lint", shellResult(0, ""), true, false},
		{"failed test", "go test ./...", shellResult(1, "--- FAIL: TestX\nFAIL"), false, true},
		{"failure hidden by a pipe", "pytest -q 2>&1 | tail -5", shellResult(0, "1 failed, 3 passed"), false, true},
		// The exit status is the pipe's last command, and tail may have
		// cut a failure off: unknown, so the reminder stays.
		{"pass through a pipe", "pytest -q | tail -5", shellResult(0, "4 passed in 0.1s"), false, false},
		{"grep finds no failure", "go test ./... | grep FAIL", shellResult(1, ""), false, false},
		{"masked by || true", "go test ./... >/dev/null 2>&1 || true", shellResult(0, ""), false, false},
		{"masked, but the output says", "go test ./... || true", shellResult(0, "--- FAIL: TestX\nFAIL"), false, true},
		{"masked by ;", "go test ./...; echo done", shellResult(0, "ok  x\ndone"), false, false},
		{"an earlier check masked by ;", "go vet ./... ; go test ./...", shellResult(0, "ok  x"), false, false},
		{"in the background", "go test ./... &", shellResult(0, ""), false, false},
		{"redirected output", "go test ./... > /tmp/out.txt 2>&1", shellResult(0, ""), true, false},
		{"redirected output failing", "go test ./... > /tmp/out.txt 2>&1", shellResult(1, ""), false, true},
		{"pipefail", "set -o pipefail; go test ./... | tail -3", shellResult(0, "ok  x"), true, false},
		{"pipefail failing", "set -euo pipefail\ngo test ./... | tail -3", shellResult(1, "ok  y"), false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := &Agent{verify: true, unverified: []string{"/ws/x.go"}}
			a.noteVerify(shellCall(c.cmd), c.result)
			if cleared := len(a.unverified) == 0; cleared != c.cleared || a.verifyFailed != c.failed {
				t.Fatalf("cleared=%v failed=%v, want %v %v", cleared, a.verifyFailed, c.cleared, c.failed)
			}
		})
	}
}

func TestReminderAfterAFailedCheckSaysSo(t *testing.T) {
	prov := &mockProvider{}
	a, _, ws := verifyAgent(t, prov)
	a.tools.Register(&failProbe{})
	prov.streams = []streamScript{
		callRound("w", "write", writeArgs(filepath.Join(ws, "x.go"))),
		callRound("s", "shell", `{"cmd":"go test ./..."}`), textRound("done"), textRound("noted"),
	}
	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	if prov.calls != 4 {
		t.Fatalf("a failed check still gets the reminder: %d calls", prov.calls)
	}
	if nudge := lastUser(prov.streamMsgs[3]); !strings.Contains(nudge, "failed") {
		t.Fatalf("nudge %q", nudge)
	}
}

// failProbe is a shell whose every command fails.
type failProbe struct{ sbProbe }

func (p *failProbe) Call(_ json.RawMessage, _ *tools.ToolCtx) (string, error) {
	return "", tools.WithContent(`{"status":"failed","exit_code":1,"output":"--- FAIL: TestX"}`, "exit 1")
}
