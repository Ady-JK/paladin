package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteToolRefusesClobber(t *testing.T) {
	ctx, ws := testCtx(t)
	tool := NewWriteTool(NewEngine())
	if _, err := tool.Call(json.RawMessage(`{"path":"`+filepath.Join(ws, "a.txt")+`","content":"a"}`), ctx); err != nil {
		t.Fatal(err)
	}
	_, err := tool.Call(json.RawMessage(`{"path":"`+filepath.Join(ws, "a.txt")+`","content":"b"}`), ctx)
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

func TestWriteEngineEdges(t *testing.T) {
	dir := t.TempDir()
	ws := filepath.Join(dir, "ws")
	engine := NewEngine()
	// Empty path.
	if _, err := engine.Write(ws, "  ", "x", false); err == nil {
		t.Fatal("empty path accepted")
	}
	// Write through an existing directory path.
	if err := os.MkdirAll(filepath.Join(ws, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Write(ws, filepath.Join(ws, "d"), "x", false); err == nil {
		t.Fatal("directory write accepted")
	}
	// Nested parents are created.
	res, err := engine.Write(ws, filepath.Join(ws, "a/b/c.txt"), "hi\nthere\n", false)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Created || res.Overwritten || res.Lines != 2 || res.Bytes != 9 {
		t.Fatalf("got %+v", res)
	}
	// Overwrite replaces.
	res, err = engine.Write(ws, filepath.Join(ws, "a/b/c.txt"), "new", true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Created || !res.Overwritten {
		t.Fatalf("got %+v", res)
	}
	// Tool-level clobber error carries exists:true.
	ctx := &ToolCtx{Workspace: ws, SessionDir: dir, Cancel: context.Background()}
	tool := NewWriteTool(engine)
	if _, err := tool.Call(json.RawMessage(`{"path":"`+filepath.Join(ws, "a/b/c.txt")+`","content":"x"}`), ctx); err == nil {
		t.Fatal("clobber accepted")
	}
	if _, err := tool.Call(json.RawMessage(`{"path":"","content":"x"}`), ctx); err == nil {
		t.Fatal("empty path accepted")
	}
}
