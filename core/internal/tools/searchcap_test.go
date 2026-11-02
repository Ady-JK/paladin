// searchcap_test.go covers the uniform output budget: grep/glob/list
// answers are cut at 50 KiB with guidance, so one broad query cannot
// flood the context. Small answers pass through untouched.

package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCutSearchOutputPassesSmallThrough(t *testing.T) {
	out, cut := cutSearchOutput("a\nb\n")
	if cut || out != "a\nb\n" {
		t.Fatalf("small output must pass through: %q %v", out, cut)
	}
}

func TestCutSearchOutputCutsAtLineBoundary(t *testing.T) {
	line := strings.Repeat("x", 1000) + "\n"
	big := strings.Repeat(line, 60) // ~60 KiB
	out, cut := cutSearchOutput(big)
	if !cut {
		t.Fatal("want a cut")
	}
	if len(out) > searchMaxOutputBytes+512 {
		t.Fatalf("output not bounded: %d", len(out))
	}
	if !strings.HasSuffix(strings.TrimSuffix(out, "\n[truncated at 50 KiB: use a more specific path, glob, or narrower pattern]\n"), "\n") &&
		!strings.Contains(out, "[truncated at 50 KiB") {
		t.Fatalf("cut must land on a line boundary with guidance: %.120q", out)
	}
	if strings.Count(out, "\n") >= 60 {
		t.Fatalf("no lines were cut: %d newlines", strings.Count(out, "\n"))
	}
}

func TestGrepCapsHugeOutput(t *testing.T) {
	dir := t.TempDir()
	ws := filepath.Join(dir, "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for i := 0; i < 1100; i++ {
		b.WriteString("needle " + strings.Repeat("y", 290) + "\n")
	}
	if err := os.WriteFile(filepath.Join(ws, "big.txt"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := &ToolCtx{Workspace: ws, SessionDir: dir, Cancel: context.Background()}
	tool := GrepTool{}
	raw, err := tool.Call(json.RawMessage(`{"path":"`+ws+`","pattern":"needle","limit":1000}`), ctx)
	if err != nil {
		t.Fatalf("grep failed: %v", err)
	}
	var resp SearchResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("response not JSON: %v", err)
	}
	if !resp.Truncated {
		t.Fatal("huge grep output must be marked truncated")
	}
	if !strings.Contains(resp.Output, "more specific path") {
		t.Fatalf("truncated output must carry guidance: %.200q", resp.Output)
	}
	if len(resp.Output) > searchMaxOutputBytes+1024 {
		t.Fatalf("grep output not bounded: %d", len(resp.Output))
	}
}
