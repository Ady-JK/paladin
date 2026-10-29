package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/sandbox"
	"github.com/Ady-JK/paladin/core/internal/sandbox/sandboxtest"
)

func sandboxCtx(t *testing.T) (*ToolCtx, string, string) {
	t.Helper()
	if sandbox.Kind() == "" {
		t.Skip("no OS sandbox")
	}
	sandboxtest.Helper(t)
	ctx, ws := testCtx(t)
	outside := t.TempDir()
	ctx.Sandbox = &sandbox.Policy{Mode: sandbox.ModeWorkspaceWrite, Writable: []string{ws}, NoTemp: true}
	return ctx, ws, outside
}

func TestSandboxedShellWritesInsideOnly(t *testing.T) {
	ctx, ws, outside := sandboxCtx(t)
	tool := NewShellTool()
	if _, err := tool.Call(json.RawMessage(`{"cmd":"echo hi > in.txt && cat in.txt"}`), ctx); err != nil {
		t.Fatalf("inside write: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(ws, "in.txt")); string(b) != "hi\n" {
		t.Fatalf("got %q", b)
	}
	target := filepath.Join(outside, "x.txt")
	_, err := tool.Call(json.RawMessage(`{"cmd":"echo bad > `+target+`"}`), ctx)
	if err == nil {
		t.Fatal("outside write should fail")
	}
	content := err.(*ToolError).ResultContent()
	if !SandboxBlocked(content) {
		t.Fatalf("missing sandbox_blocked flag: %s", content)
	}
	if _, statErr := os.Stat(target); statErr == nil {
		t.Fatal("file escaped the sandbox")
	}
}

func TestPlainFailureIsNotSandboxBlocked(t *testing.T) {
	ctx, _, _ := sandboxCtx(t)
	_, err := NewShellTool().Call(json.RawMessage(`{"cmd":"cat does-not-exist"}`), ctx)
	if err == nil || SandboxBlocked(err.(*ToolError).ResultContent()) {
		t.Fatalf("plain failure flagged: %v", err)
	}
}

func TestUnsandboxedFailureNeverFlagged(t *testing.T) {
	ctx, _ := testCtx(t)
	_, err := NewShellTool().Call(json.RawMessage(`{"cmd":"echo 'Operation not permitted' >&2; exit 1"}`), ctx)
	if err == nil || SandboxBlocked(err.(*ToolError).ResultContent()) {
		t.Fatal("no sandbox, no flag")
	}
}

func TestSandboxedBackgroundJobIsConfined(t *testing.T) {
	ctx, _, outside := sandboxCtx(t)
	target := filepath.Join(outside, "bg.txt")
	out, err := NewShellTool().Call(json.RawMessage(`{"cmd":"echo bad > `+target+`; echo done","background":true}`), ctx)
	if err != nil {
		t.Fatal(err)
	}
	var resp ShellResponse
	_ = json.Unmarshal([]byte(out), &resp)
	tool := NewShellTool()
	_ = tool
	for i := 0; i < 100; i++ {
		sleepMs(20)
		if _, statErr := os.Stat(target); statErr == nil {
			t.Fatal("background job escaped the sandbox")
		}
	}
}
