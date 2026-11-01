package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func nestedOf(t *testing.T, content string) string {
	t.Helper()
	var obj map[string]any
	if err := json.Unmarshal([]byte(content), &obj); err != nil {
		t.Fatalf("result is not a JSON object: %v", err)
	}
	s, _ := obj["nested_instructions"].(string)
	return s
}

func callRead(t *testing.T, r *Registry, ctx *ToolCtx, path string) ToolResult {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"path": path})
	res := r.Call(&ToolCall{ID: "r", Name: "read", Arguments: string(raw)}, ctx)
	if res.IsError {
		t.Fatalf("read %s: %s", path, res.Content)
	}
	return res
}

func TestFirstTouchOfASubfolderAttachesItsInstructions(t *testing.T) {
	ctx, ws := testCtx(t)
	writeTree(t, ws, map[string]string{
		"AGENTS.md":                 "root rules",
		"top.go":                    "package top\n",
		"svc/AGENTS.md":             "svc rules",
		"svc/api/CLAUDE.md":         "api rules",
		"svc/api/handler.go":        "package api\n",
		"svc/api/more.go":           "package api\n",
		"svc/db/AGENTS.override.md": "db override",
		"svc/db/AGENTS.md":          "db plain",
		"svc/db/store.go":           "package db\n",
	})
	r := BuiltinRegistry()
	if got := nestedOf(t, callRead(t, r, ctx, filepath.Join(ws, "top.go")).Content); got != "" {
		t.Fatalf("workspace-root instructions are already in the system prompt: %q", got)
	}
	got := nestedOf(t, callRead(t, r, ctx, filepath.Join(ws, "svc", "api", "handler.go")).Content)
	svc, api := strings.Index(got, "svc rules"), strings.Index(got, "api rules")
	if svc < 0 || api < svc || strings.Contains(got, "root rules") {
		t.Fatalf("want svc then api rules, got %q", got)
	}
	if !strings.Contains(got, filepath.Join(ws, "svc", "api", "CLAUDE.md")) {
		t.Fatalf("each file is named by path: %q", got)
	}
	if got := nestedOf(t, callRead(t, r, ctx, filepath.Join(ws, "svc", "api", "more.go")).Content); got != "" {
		t.Fatalf("already attached this session: %q", got)
	}
	got = nestedOf(t, callRead(t, r, ctx, filepath.Join(ws, "svc", "db", "store.go")).Content)
	if !strings.Contains(got, "db override") || strings.Contains(got, "db plain") || strings.Contains(got, "svc rules") {
		t.Fatalf("override wins and svc is not repeated: %q", got)
	}
	// The record lives with the session, so a fresh registry (a restart)
	// does not repeat it, while another session gets it again.
	if got := nestedOf(t, callRead(t, BuiltinRegistry(), ctx, filepath.Join(ws, "svc", "api", "handler.go")).Content); got != "" {
		t.Fatalf("repeated after restart: %q", got)
	}
	other := *ctx
	other.SessionDir = filepath.Join(t.TempDir(), "sess2")
	if got := nestedOf(t, callRead(t, r, &other, filepath.Join(ws, "svc", "api", "handler.go")).Content); !strings.Contains(got, "api rules") {
		t.Fatalf("a new session gets them again: %q", got)
	}
}

func TestNestedInstructionsSkipOutsideAndFailedCalls(t *testing.T) {
	ctx, ws := testCtx(t)
	outside := filepath.Join(filepath.Dir(ws), "other")
	writeTree(t, outside, map[string]string{"sub/AGENTS.md": "nope", "sub/x.go": "package x\n"})
	writeTree(t, ws, map[string]string{"pkg/AGENTS.md": "pkg rules"})
	r := BuiltinRegistry()
	if got := nestedOf(t, callRead(t, r, ctx, filepath.Join(outside, "sub", "x.go")).Content); got != "" {
		t.Fatalf("outside the workspace: %q", got)
	}
	res := r.Call(&ToolCall{ID: "e", Name: "read", Arguments: `{"path":"` + filepath.Join(ws, "pkg", "missing.go") + `"}`}, ctx)
	if !res.IsError || strings.Contains(res.Content, "pkg rules") {
		t.Fatalf("a failed call attaches nothing: %+v", res)
	}
	raw, _ := json.Marshal(map[string]any{"path": filepath.Join(ws, "pkg", "new.go"), "content": "package pkg\n"})
	res = r.Call(&ToolCall{ID: "w", Name: "write", Arguments: string(raw)}, ctx)
	if res.IsError || !strings.Contains(nestedOf(t, res.Content), "pkg rules") {
		t.Fatalf("write into a new folder attaches its rules: %+v", res)
	}
}

func TestNestedInstructionsStayWithinBudget(t *testing.T) {
	ctx, ws := testCtx(t)
	big := strings.Repeat("r", nestedMaxBytes+500)
	writeTree(t, ws, map[string]string{"a/AGENTS.md": big, "a/x.go": "package a\n"})
	got := nestedOf(t, callRead(t, BuiltinRegistry(), ctx, filepath.Join(ws, "a", "x.go")).Content)
	if len(got) > nestedMaxBytes+600 || !strings.Contains(got, "truncated") {
		t.Fatalf("not capped: %d bytes", len(got))
	}
	if _, err := os.Stat(filepath.Join(ctx.SessionDir, nestedRecordFile)); err != nil {
		t.Fatalf("record not kept with the session: %v", err)
	}
}

// The first write or edit into a folder with unseen instructions is held
// with them, so the agent reads the rules before it changes anything
// there; the resent call runs as usual.
func TestFirstMutationIntoASubfolderIsHeldWithItsInstructions(t *testing.T) {
	ctx, ws := testCtx(t)
	writeTree(t, ws, map[string]string{"pkg/AGENTS.md": "pkg rules", "pkg/old.go": "package pkg\n"})
	r := BuiltinRegistry()
	target := filepath.Join(ws, "pkg", "new.go")
	raw, _ := json.Marshal(map[string]any{"path": target, "content": "package pkg\n"})
	call := &ToolCall{ID: "w1", Name: "write", Arguments: string(raw)}
	held, ok := r.HoldForInstructions(call, ctx)
	if !ok || !held.IsError || !strings.Contains(nestedOf(t, held.Content), "pkg rules") || !strings.Contains(held.Content, "resend") {
		t.Fatalf("first write: %v %+v", ok, held)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("a held write must not touch the file")
	}
	if _, again := r.HoldForInstructions(call, ctx); again {
		t.Fatal("instructions are held for once per session")
	}
	res := r.Call(call, ctx)
	if res.IsError || nestedOf(t, res.Content) != "" {
		t.Fatalf("the resent write runs without repeating them: %+v", res)
	}
	edit, _ := json.Marshal(map[string]any{"path": filepath.Join(ws, "pkg", "old.go"), "old_string": "pkg", "new_string": "pkg2"})
	if _, held := r.HoldForInstructions(&ToolCall{ID: "e", Name: "edit", Arguments: string(edit)}, ctx); held {
		t.Fatal("seen folders are not held again")
	}
	// Reads, outside paths and folders without instructions run at once.
	writeTree(t, ws, map[string]string{"lib/AGENTS.md": "lib rules", "plain/x.go": "package plain\n"})
	for _, c := range []*ToolCall{
		{Name: "read", Arguments: `{"path":"` + filepath.Join(ws, "lib", "AGENTS.md") + `"}`},
		{Name: "write", Arguments: `{"path":"` + filepath.Join(ws, "plain", "y.go") + `","content":"x"}`},
		{Name: "write", Arguments: `{"path":"` + filepath.Join(t.TempDir(), "sub", "z.go") + `","content":"x"}`},
	} {
		if _, held := r.HoldForInstructions(c, ctx); held {
			t.Fatalf("held %s %s", c.Name, c.Arguments)
		}
	}
}

// An instruction file that cannot be read is never recorded as seen:
// the write is held once with its name, and once it becomes readable
// its rules are delivered before the next change there.
func TestUnreadableInstructionsAreNamedNotMarkedSeen(t *testing.T) {
	ctx, ws := testCtx(t)
	writeTree(t, ws, map[string]string{"locked/AGENTS.md": "locked rules"})
	rules := filepath.Join(ws, "locked", "AGENTS.md")
	if err := os.Chmod(rules, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(rules, 0o644) })
	if _, err := os.ReadFile(rules); err == nil {
		t.Skip("running as a user who can read anything")
	}
	r := BuiltinRegistry()
	call := heldWrite(filepath.Join(ws, "locked", "a.go"))
	held, ok := r.HoldForInstructions(call, ctx)
	if !ok || !strings.Contains(held.Content, rules) || !strings.Contains(held.Content, "could not be read") {
		t.Fatalf("first write: %v %s", ok, held.Content)
	}
	if rec := nestedRecordOf(t, ctx); slices.Contains(rec.Files, rules) {
		t.Fatalf("an unread file was recorded as seen: %+v", rec)
	}
	if _, again := r.HoldForInstructions(call, ctx); again {
		t.Fatal("the resent write runs once the agent has been told")
	}
	os.Chmod(rules, 0o644)
	held, ok = r.HoldForInstructions(heldWrite(filepath.Join(ws, "locked", "b.go")), ctx)
	if !ok || !strings.Contains(nestedOf(t, held.Content), "locked rules") {
		t.Fatalf("rules that became readable: %v %s", ok, held.Content)
	}
}

// When the session's budget is used up, a folder's instructions are not
// silently skipped: writes there stay held until the agent reads the
// file itself, and an oversized file arrives cut, saying so.
func TestInstructionsOverTheBudgetHoldUntilRead(t *testing.T) {
	ctx, ws := testCtx(t)
	writeTree(t, ws, map[string]string{
		"big/AGENTS.md":  strings.Repeat("r", nestedMaxBytes+500),
		"next/AGENTS.md": "next rules",
	})
	r := BuiltinRegistry()
	held, ok := r.HoldForInstructions(heldWrite(filepath.Join(ws, "big", "a.go")), ctx)
	if !ok || !strings.Contains(nestedOf(t, held.Content), "truncated") {
		t.Fatalf("oversized: %v %.200s", ok, held.Content)
	}
	if _, again := r.HoldForInstructions(heldWrite(filepath.Join(ws, "big", "a.go")), ctx); again {
		t.Fatal("a cut file still counts as delivered")
	}
	next := filepath.Join(ws, "next", "AGENTS.md")
	call := heldWrite(filepath.Join(ws, "next", "a.go"))
	for range 2 {
		held, ok = r.HoldForInstructions(call, ctx)
		if !ok || !strings.Contains(held.Content, next) || !strings.Contains(held.Content, "budget") {
			t.Fatalf("over budget: %v %s", ok, held.Content)
		}
	}
	callRead(t, r, ctx, next)
	if _, again := r.HoldForInstructions(call, ctx); again {
		t.Fatal("reading the file delivers it")
	}
}

func heldWrite(path string) *ToolCall {
	raw, _ := json.Marshal(map[string]any{"path": path, "content": "package x\n"})
	return &ToolCall{ID: "w", Name: "write", Arguments: string(raw)}
}

func nestedRecordOf(t *testing.T, ctx *ToolCtx) nestedRecord {
	t.Helper()
	var rec nestedRecord
	data, _ := os.ReadFile(filepath.Join(ctx.SessionDir, nestedRecordFile))
	_ = json.Unmarshal(data, &rec)
	return rec
}
