package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func testCtx(t *testing.T) (*ToolCtx, string) {
	t.Helper()
	dir := t.TempDir()
	ws := filepath.Join(dir, "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	return &ToolCtx{Workspace: ws, SessionDir: filepath.Join(dir, "sess"), Cancel: context.Background()}, ws
}

func TestUnknownToolIsErrorResult(t *testing.T) {
	r := NewRegistry()
	result := r.Call(&ToolCall{ID: "1", Name: "missing", Arguments: "{}"}, &ToolCtx{})
	if !result.IsError {
		t.Fatal("expected error")
	}
}

func TestDefinitionsSortedByName(t *testing.T) {
	r := NewRegistry()
	r.Register(ReadTool{})
	if len(r.Definitions()) != 1 {
		t.Fatal("expected one definition")
	}
	if got := BuiltinRegistry().Len(); got != 4 {
		t.Fatalf("expected 4 builtin tools, got %d", got)
	}
}

func TestRejectsEscapeAndBareRelative(t *testing.T) {
	if _, _, err := ResolveScopedPath("read", "path", "workspace/../etc/hosts", "/tmp/ws"); err == nil {
		t.Fatal("expected escape rejection")
	}
	if _, _, err := ResolveScopedPath("read", "path", "src/main.rs", "/tmp/ws"); err == nil {
		t.Fatal("expected bare relative rejection")
	}
	resolved, scope, err := ResolveScopedPath("read", "path", "workspace/src/main.rs", "/tmp/ws")
	if err != nil {
		t.Fatal(err)
	}
	if scope != ScopeInside || resolved != "/tmp/ws/src/main.rs" {
		t.Fatalf("got %q %q", resolved, scope)
	}
	resolved, scope, err = ResolveScopedPath("read", "path", "/etc/hosts", "/tmp/ws")
	if err != nil {
		t.Fatal(err)
	}
	if scope != ScopeOutside || resolved != "/etc/hosts" {
		t.Fatalf("got %q %q", resolved, scope)
	}
}

func TestReadsWithOffsetAndLimit(t *testing.T) {
	ctx, ws := testCtx(t)
	if err := os.WriteFile(filepath.Join(ws, "a.txt"), []byte("l1\nl2\nl3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := (ReadTool{}).Call(json.RawMessage(`{"path":"workspace/a.txt","offset":2,"limit":1}`), ctx)
	if err != nil {
		t.Fatal(err)
	}
	var resp ReadResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Content != "l2\n" || !resp.HasMore || resp.StopReason != "line_limit" {
		t.Fatalf("got %+v", resp)
	}
}

func TestRefusesBinaryFiles(t *testing.T) {
	ctx, ws := testCtx(t)
	if err := os.WriteFile(filepath.Join(ws, "b.bin"), []byte{0, 1, 2}, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := (ReadTool{}).Call(json.RawMessage(`{"path":"workspace/b.bin"}`), ctx); err == nil {
		t.Fatal("expected binary rejection")
	}
}

func TestRejectsBareRelativePaths(t *testing.T) {
	ctx, _ := testCtx(t)
	if _, err := (ReadTool{}).Call(json.RawMessage(`{"path":"a.txt"}`), ctx); err == nil {
		t.Fatal("expected rejection")
	}
}

func TestWriteThenEditRoundtrip(t *testing.T) {
	dir := t.TempDir()
	ws := filepath.Join(dir, "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	engine := NewEngine()
	if _, err := engine.Write(ws, "workspace/a.txt", "hello\n", false); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Write(ws, "workspace/a.txt", "x", false); err == nil {
		t.Fatal("expected exists error")
	} else if ee, ok := err.(*EngineError); !ok || ee.Kind != "exists" {
		t.Fatalf("expected exists, got %v", err)
	}
	res, err := engine.Edit(ws, "workspace/a.txt", "hello", "bye", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Replacements != 1 {
		t.Fatalf("got %+v", res)
	}
	data, err := os.ReadFile(filepath.Join(ws, "a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "bye\n" {
		t.Fatalf("got %q", data)
	}
}

func TestEditAmbiguousListsOccurrences(t *testing.T) {
	dir := t.TempDir()
	ws := filepath.Join(dir, "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	engine := NewEngine()
	if _, err := engine.Write(ws, "workspace/a.txt", "x\nx\n", false); err != nil {
		t.Fatal(err)
	}
	_, err := engine.Edit(ws, "workspace/a.txt", "x", "y", false)
	ee, ok := err.(*EngineError)
	if !ok || ee.Kind != "ambiguous" || ee.Count != 2 {
		t.Fatalf("expected ambiguous count 2, got %v", err)
	}
}

func TestWriteToolRefusesClobber(t *testing.T) {
	ctx, _ := testCtx(t)
	tool := NewWriteTool(NewEngine())
	if _, err := tool.Call(json.RawMessage(`{"path":"workspace/a.txt","content":"a"}`), ctx); err != nil {
		t.Fatal(err)
	}
	_, err := tool.Call(json.RawMessage(`{"path":"workspace/a.txt","content":"b"}`), ctx)
	if err == nil {
		t.Fatal("expected clobber error")
	}
	terr, ok := err.(*ToolError)
	if !ok {
		t.Fatalf("expected ToolError, got %T", err)
	}
	var payload map[string]any
	if uerr := json.Unmarshal([]byte(terr.Content), &payload); uerr != nil {
		t.Fatal(uerr)
	}
	if payload["exists"] != true {
		t.Fatalf("got %v", payload)
	}
}

func TestEditToolRequiresOldString(t *testing.T) {
	dir := t.TempDir()
	ctx := &ToolCtx{Workspace: dir, SessionDir: dir, Cancel: context.Background()}
	tool := NewEditTool(NewEngine())
	if _, err := tool.Call(json.RawMessage(`{"path":"workspace/a.txt","old_string":"","new_string":"b"}`), ctx); err == nil {
		t.Fatal("expected error")
	}
}

func TestSyncRunReturnsOutput(t *testing.T) {
	ctx, _ := testCtx(t)
	tool := NewShellTool()
	out, err := tool.Call(json.RawMessage(`{"cmd":"echo hi"}`), ctx)
	if err != nil {
		t.Fatal(err)
	}
	var resp ShellResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Status != JobDone || resp.Output == nil || *resp.Output != "hi\n" {
		t.Fatalf("got %+v", resp)
	}
}

func TestFailingCommandIsToolError(t *testing.T) {
	ctx, _ := testCtx(t)
	tool := NewShellTool()
	_, err := tool.Call(json.RawMessage(`{"cmd":"exit 3"}`), ctx)
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); got != "shell: the command exited with status 3" && len(got) < 10 {
		t.Fatalf("got %q", got)
	}
}

func TestBackgroundStatusCancel(t *testing.T) {
	dir := t.TempDir()
	ws := filepath.Join(dir, "ws")
	sess := filepath.Join(dir, "sess")
	os.MkdirAll(ws, 0o755)
	os.MkdirAll(sess, 0o755)
	ctx := &ToolCtx{Workspace: ws, SessionDir: sess, Cancel: context.Background()}
	tool := NewShellTool()
	out, err := tool.Call(json.RawMessage(`{"cmd":"sleep 30","background":true}`), ctx)
	if err != nil {
		t.Fatal(err)
	}
	var resp ShellResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.JobID == nil {
		t.Fatal("missing job id")
	}
	statusOut, err := tool.Call(json.RawMessage(`{"action":"status","job_id":"`+*resp.JobID+`"}`), ctx)
	if err != nil {
		t.Fatal(err)
	}
	var status ShellResponse
	if err := json.Unmarshal([]byte(statusOut), &status); err != nil {
		t.Fatal(err)
	}
	if status.Status != JobRunning {
		t.Fatalf("got %+v", status)
	}
	cancelOut, err := tool.Call(json.RawMessage(`{"action":"cancel","job_id":"`+*resp.JobID+`"}`), ctx)
	if err != nil {
		t.Fatal(err)
	}
	var cancelled ShellResponse
	if err := json.Unmarshal([]byte(cancelOut), &cancelled); err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != JobCancelled {
		t.Fatalf("got %+v", cancelled)
	}
}
