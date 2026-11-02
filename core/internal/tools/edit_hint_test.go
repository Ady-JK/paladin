// edit_hint_test.go covers the miss hint: a failed old_string carries
// the closest matching lines, so the next attempt can succeed without
// re-reading the whole file.

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClosestLinesFindsWindow(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 30; i++ {
		b.WriteString("filler line number alpha\n")
	}
	b.WriteString("func targetFunction(beta string) error {\n")
	for i := 1; i <= 10; i++ {
		b.WriteString("filler line number alpha\n")
	}
	hint := closestLines(b.String(), "func targetFunction(beta string)")
	if len(hint) == 0 {
		t.Fatal("want a hint window")
	}
	found := false
	for _, ln := range hint {
		if strings.HasPrefix(ln, "31: ") && strings.Contains(ln, "targetFunction") {
			found = true
		}
	}
	if !found {
		t.Fatalf("hint misses the match line: %q", hint)
	}
	if len(hint) > 2*closestLineRadius+1 {
		t.Fatalf("hint too large: %d lines", len(hint))
	}
}

func TestClosestLinesEmptyWithoutOverlap(t *testing.T) {
	if hint := closestLines("aaa bbb ccc\n", "zzz yyy qqq"); hint != nil {
		t.Fatalf("want no hint, got %q", hint)
	}
	if hint := closestLines("aaa\n", "   \n"); hint != nil {
		t.Fatalf("blank old text must not hint, got %q", hint)
	}
}

func TestEditMissCarriesClosestLines(t *testing.T) {
	dir := t.TempDir()
	ws := filepath.Join(dir, "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hi\")\n}\n"
	if err := os.WriteFile(filepath.Join(ws, "f.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	engine := NewEngine()
	_, err := engine.Edit("", ws, filepath.Join(ws, "f.txt"), "fmt.Println(\"bye\")", "x", false)
	eeng := (*EngineError)(nil)
	if !errors.As(err, &eeng) || eeng.Kind != "notfound" {
		t.Fatalf("want notfound, got %v", err)
	}
	if len(eeng.Hint) == 0 {
		t.Fatal("miss must carry closest lines")
	}
	joined := strings.Join(eeng.Hint, "\n")
	if !strings.Contains(joined, "Println") {
		t.Fatalf("hint misses the neighborhood: %q", joined)
	}
	// Tool-level payload exposes the hint as JSON.
	tool := NewEditTool(engine)
	ctx := &ToolCtx{Workspace: ws, SessionDir: dir, Cancel: context.Background()}
	_, terr := tool.Call(json.RawMessage(`{"path":"`+filepath.Join(ws, "f.txt")+`","old_string":"fmt.Println(\"bye\")","new_string":"x"}`), ctx)
	toolErr, ok := terr.(*ToolError)
	if !ok || toolErr.Content == "" {
		t.Fatalf("expected content-carrying error, got %v", terr)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(toolErr.Content), &payload); err != nil {
		t.Fatalf("payload not JSON: %v", err)
	}
	lines, ok := payload["closest_lines"].([]any)
	if !ok || len(lines) == 0 {
		t.Fatalf("payload lacks closest_lines: %v", payload)
	}
}
