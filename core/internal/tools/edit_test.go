package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditToolRequiresOldString(t *testing.T) {
	dir := t.TempDir()
	ctx := &ToolCtx{Workspace: dir, SessionDir: dir, Cancel: context.Background()}
	tool := NewEditTool(NewEngine())
	if _, err := tool.Call(json.RawMessage(`{"path":"`+filepath.Join(dir, "a.txt")+`","old_string":"","new_string":"b"}`), ctx); err == nil {
		t.Fatal("expected error")
	}
}

func TestEditEngineEdges(t *testing.T) {
	dir := t.TempDir()
	ws := filepath.Join(dir, "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	engine := NewEngine()
	// Missing file.
	if _, err := engine.Edit(ws, filepath.Join(ws, "nope.txt"), "a", "b", false); err == nil {
		t.Fatal("missing accepted")
	}
	// Binary rejected.
	if err := os.WriteFile(filepath.Join(ws, "bin"), []byte{0, 1, 2}, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Edit(ws, filepath.Join(ws, "bin"), "a", "b", false); err == nil {
		t.Fatal("binary accepted")
	}
	// Not found text.
	if _, err := engine.Write(ws, filepath.Join(ws, "f.txt"), "hello\n", false); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Edit(ws, filepath.Join(ws, "f.txt"), "nope", "b", false); err == nil {
		t.Fatal("missing text accepted")
	}
	// replace_all replaces every occurrence.
	res, err := engine.Edit(ws, filepath.Join(ws, "f.txt"), "l", "L", true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Replacements != 2 || len(res.Matches) != 2 {
		t.Fatalf("got %+v", res)
	}
	// Tool-level notfound and ambiguous carry structured content.
	tool := NewEditTool(engine)
	if _, err := tool.Call(json.RawMessage(`{"path":"`+filepath.Join(ws, "f.txt")+`","old_string":"zzz","new_string":"y"}`), ctx2(dir, ws)); err == nil {
		t.Fatal("notfound accepted")
	} else if terr, ok := err.(*ToolError); !ok || terr.Content == "" {
		t.Fatalf("expected content-carrying error, got %v", err)
	}
	if _, err := engine.Write(ws, filepath.Join(ws, "g.txt"), "x\nx\n", false); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Call(json.RawMessage(`{"path":"`+filepath.Join(ws, "g.txt")+`","old_string":"x","new_string":"y"}`), ctx2(dir, ws)); err == nil {
		t.Fatal("ambiguous accepted")
	} else if terr, ok := err.(*ToolError); !ok || !strings.Contains(terr.Content, "occurrences") {
		t.Fatalf("expected occurrences payload, got %v", err)
	}
	// Over-limit file rejected.
	big := make([]byte, MaxFileBytes+1)
	if err := os.WriteFile(filepath.Join(ws, "huge.txt"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Edit(ws, filepath.Join(ws, "huge.txt"), "a", "b", false); err == nil {
		t.Fatal("huge file accepted")
	}
}

func ctx2(dir, ws string) *ToolCtx {
	return &ToolCtx{Workspace: ws, SessionDir: dir, Cancel: context.Background()}
}
