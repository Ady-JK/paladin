package tools

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func diagCtx(t *testing.T, d Diagnosis) (*ToolCtx, string, *[]string) {
	t.Helper()
	ctx, ws := testCtx(t)
	ctx.SessionID = "diag-" + t.Name()
	var mu sync.Mutex
	var calls []string
	ctx.Diagnose = func(path string, _ func(string) bool) Diagnosis {
		mu.Lock()
		calls = append(calls, path)
		mu.Unlock()
		return d
	}
	return ctx, ws, &calls
}

func TestWriteAndEditReportLanguageServerErrors(t *testing.T) {
	d := Diagnosis{Server: "gopls", Errors: 3, Diagnostics: []Diagnostic{
		{Line: 3, Column: 2, Message: "undefined: x", Source: "compiler"},
		{Line: 5, Column: 1, Message: "missing return"},
	}}
	ctx, ws, calls := diagCtx(t, d)
	path := filepath.Join(ws, "a.go")
	res := writeCall(t, ctx, path, "package a\n")
	diags, _ := res["diagnostics"].([]any)
	note, _ := res["note"].(string)
	if len(diags) != 2 || res["diagnostics_from"] != "gopls" || res["errors"] != float64(3) {
		t.Fatalf("write result: %+v", res)
	}
	if !strings.Contains(note, "gopls reports 3 errors") || !strings.Contains(note, "showing 2") {
		t.Fatalf("note: %q", note)
	}
	raw, _ := json.Marshal(map[string]any{"path": path, "old_string": "package a", "new_string": "package b"})
	out, err := NewEditTool(NewEngine()).Call(raw, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"diagnostics_from":"gopls"`) || !strings.Contains(out, "undefined: x") {
		t.Fatalf("edit result: %s", out)
	}
	if len(*calls) != 2 || (*calls)[0] != path {
		t.Fatalf("diagnose calls: %v", *calls)
	}
}

func TestCleanOrUncheckedFilesAddNothing(t *testing.T) {
	ctx, ws, calls := diagCtx(t, Diagnosis{Server: "gopls"})
	if res := writeCall(t, ctx, filepath.Join(ws, "a.go"), "package a\n"); res["diagnostics"] != nil || res["note"] != nil {
		t.Fatalf("no errors, nothing added: %+v", res)
	}
	outside := filepath.Join(filepath.Dir(ws), "elsewhere", "b.go")
	writeCall(t, ctx, outside, "package b\n")
	if len(*calls) != 1 {
		t.Fatalf("files outside the workspace are not checked: %v", *calls)
	}
}

func TestReadWarmsTheLanguageServer(t *testing.T) {
	ctx, ws := testCtx(t)
	writeTree(t, ws, map[string]string{"a.go": "package a\n"})
	var warmed []string
	ctx.Warm = func(path string, _ func(string) bool) { warmed = append(warmed, path) }
	if _, err := (ReadTool{}).Call(json.RawMessage(`{"path":"`+filepath.Join(ws, "a.go")+`"}`), ctx); err != nil {
		t.Fatal(err)
	}
	if len(warmed) != 1 || warmed[0] != filepath.Join(ws, "a.go") {
		t.Fatalf("warmed %v", warmed)
	}
}
