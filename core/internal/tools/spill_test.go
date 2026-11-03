package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestShellKeepsOversizedOutputInTheJobLog(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses seq")
	}
	ctx, _ := testCtx(t)
	ctx.CallID = "call/1"
	out, err := NewShellTool().Call(json.RawMessage(`{"cmd":"seq 1 20000"}`), ctx)
	if err != nil {
		t.Fatal(err)
	}
	var resp ShellResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Truncated == nil || !*resp.Truncated || resp.OutputFile == nil {
		t.Fatalf("expected a truncated result naming the log: %s", out[:200])
	}
	if !strings.Contains(*resp.Output, *resp.OutputFile) {
		t.Fatal("the truncation marker must name the full output file")
	}
	full, err := os.ReadFile(*resp.OutputFile)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for i := 1; i <= 20000; i++ {
		fmt.Fprintf(&b, "%d\n", i)
	}
	if string(full) != b.String() {
		t.Fatalf("log holds %d bytes, want the whole %d-byte output", len(full), b.Len())
	}
}

func TestShellSmallOutputDoesNotSpill(t *testing.T) {
	ctx, _ := testCtx(t)
	ctx.CallID = "c2"
	out, err := NewShellTool().Call(json.RawMessage(`{"cmd":"echo hi"}`), ctx)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "output_file") {
		t.Fatalf("small output spilled: %s", out)
	}
	if _, err := os.Stat(filepath.Join(ctx.SessionDir, ToolOutputDir)); !os.IsNotExist(err) {
		t.Fatalf("tool-output folder created for a small result: %v", err)
	}
}

func TestSpillTextNamesItsFile(t *testing.T) {
	ctx, _ := testCtx(t)
	ctx.CallID = "mcp-9"
	path := SpillText(ctx, "full text")
	if path != filepath.Join(ctx.SessionDir, ToolOutputDir, "mcp-9.txt") {
		t.Fatalf("path %q", path)
	}
	if data, _ := os.ReadFile(path); string(data) != "full text" {
		t.Fatalf("content %q", data)
	}
	if SpillText(&ToolCtx{}, "x") != "" {
		t.Fatal("no session means no spill")
	}
}
