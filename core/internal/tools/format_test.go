package tools

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/sandbox"
	"github.com/Ady-JK/paladin/core/internal/sandbox/sandboxtest"
)

func formatCtx(t *testing.T) (*ToolCtx, string) {
	t.Helper()
	// Formatters run inside the OS sandbox, which re-execs the helper
	// binary; under go test that would be this test binary, recursively.
	if sandbox.Kind() != "" {
		sandboxtest.Helper(t)
	}
	ctx, ws := testCtx(t)
	ctx.SessionID = "fmt-" + t.Name()
	ctx.FormatOnWrite = true
	return ctx, ws
}

// fakeFormatter installs an executable script that upper-cases stdin.
func fakeFormatter(t *testing.T, path string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script formatter")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\ntr a-z A-Z\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeCall(t *testing.T, ctx *ToolCtx, path, content string) map[string]any {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"path": path, "content": content, "overwrite": true})
	out, err := NewWriteTool(NewEngine()).Call(raw, ctx)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	var res map[string]any
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	return res
}

func TestWriteFormatsGoWithGofmt(t *testing.T) {
	if _, err := exec.LookPath("gofmt"); err != nil {
		t.Skip("gofmt not installed")
	}
	ctx, ws := formatCtx(t)
	path := filepath.Join(ws, "a.go")
	res := writeCall(t, ctx, path, "package a\nfunc  F( ) {return}\n")
	if res["formatted"] != "gofmt" {
		t.Fatalf("expected gofmt to run: %+v", res)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "package a\n\nfunc F() { return }\n" {
		t.Fatalf("not formatted: %q", data)
	}
	if note, _ := res["note"].(string); !strings.Contains(note, "re-read") {
		t.Fatalf("the model is told the file changed: %+v", res)
	}
	// The formatted version counts as seen, so overwriting works at once.
	writeCall(t, ctx, path, "package a\n")
	// Already formatted content reports nothing.
	if res := writeCall(t, ctx, path, "package a\n"); res["formatted"] != nil {
		t.Fatalf("no change, no report: %+v", res)
	}
}

func TestEditFormatsWithTheProjectsPrettier(t *testing.T) {
	ctx, ws := formatCtx(t)
	fakeFormatter(t, filepath.Join(ws, "node_modules", ".bin", "prettier"))
	path := filepath.Join(ws, "web", "app.ts")
	writeTree(t, ws, map[string]string{"web/app.ts": "let a = 1\n"})
	if _, err := (ReadTool{}).Call(json.RawMessage(`{"path":"`+path+`"}`), ctx); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"path": path, "old_string": "1", "new_string": "2"})
	out, err := NewEditTool(NewEngine()).Call(raw, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"formatted":"prettier"`) {
		t.Fatalf("prettier from node_modules should run: %s", out)
	}
	if data, _ := os.ReadFile(path); string(data) != "LET A = 2\n" {
		t.Fatalf("got %q", data)
	}
}

func TestPythonFormatsOnlyWhenTheProjectOptsIn(t *testing.T) {
	ctx, ws := formatCtx(t)
	fakeFormatter(t, filepath.Join(ws, ".venv", "bin", "ruff"))
	path := filepath.Join(ws, "pkg", "m.py")
	if res := writeCall(t, ctx, path, "x = 1\n"); res["formatted"] != nil {
		t.Fatalf("no [tool.ruff] config, so no formatting: %+v", res)
	}
	writeTree(t, ws, map[string]string{"pyproject.toml": "[project]\nname='x'\n\n[tool.ruff]\nline-length = 100\n"})
	if res := writeCall(t, ctx, path, "y = 2\n"); res["formatted"] != "ruff" {
		t.Fatalf("ruff configured in pyproject: %+v", res)
	}
}

func TestFormattingSkipsOutsideOffAndFailures(t *testing.T) {
	ctx, ws := formatCtx(t)
	fakeFormatter(t, filepath.Join(ws, "node_modules", ".bin", "prettier"))
	outside := filepath.Join(filepath.Dir(ws), "elsewhere", "o.ts")
	if res := writeCall(t, ctx, outside, "x\n"); res["formatted"] != nil {
		t.Fatalf("files outside the workspace are never formatted: %+v", res)
	}
	ctx.FormatOnWrite = false
	if res := writeCall(t, ctx, filepath.Join(ws, "a.ts"), "x\n"); res["formatted"] != nil {
		t.Fatalf("switched off: %+v", res)
	}
	ctx.FormatOnWrite = true
	broken := filepath.Join(ws, "node_modules", ".bin", "prettier")
	if err := os.WriteFile(broken, []byte("#!/bin/sh\necho boom >&2\nexit 2\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(ws, "b.ts")
	if res := writeCall(t, ctx, path, "keep me\n"); res["formatted"] != nil {
		t.Fatalf("a failing formatter leaves the file alone: %+v", res)
	}
	if data, _ := os.ReadFile(path); string(data) != "keep me\n" {
		t.Fatalf("got %q", data)
	}
}

// A formatter the project ships runs only when its tools are trusted;
// otherwise the file is left as written.
func TestFormatterTheProjectShipsNeedsTrust(t *testing.T) {
	ctx, ws := formatCtx(t)
	bin := filepath.Join(ws, "node_modules", ".bin", "prettier")
	os.MkdirAll(filepath.Dir(bin), 0o755)
	os.WriteFile(bin, []byte("#!/bin/sh\ncat >/dev/null\necho formatted\n"), 0o755)
	var asked []string
	ctx.TrustProject = func(tool string) bool { asked = append(asked, tool); return false }
	target := filepath.Join(ws, "a.js")
	r := BuiltinRegistry()
	raw, _ := json.Marshal(map[string]any{"path": target, "content": "x  =  1\n"})
	if res := r.Call(&ToolCall{ID: "w", Name: "write", Arguments: string(raw)}, ctx); res.IsError {
		t.Fatal(res.Content)
	}
	if got, _ := os.ReadFile(target); string(got) != "x  =  1\n" || len(asked) != 1 || asked[0] != "node_modules/.bin/prettier" {
		t.Fatalf("untrusted: %q asked %v", got, asked)
	}
	ctx.TrustProject = func(string) bool { return true }
	target = filepath.Join(ws, "b.js")
	raw, _ = json.Marshal(map[string]any{"path": target, "content": "x  =  1\n"})
	if res := r.Call(&ToolCall{ID: "w2", Name: "write", Arguments: string(raw)}, ctx); res.IsError {
		t.Fatal(res.Content)
	}
	if got, _ := os.ReadFile(target); string(got) != "formatted\n" {
		t.Fatalf("trusted: %q", got)
	}
}
