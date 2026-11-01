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

func TestShellSpillsOversizedOutputToTheSession(t *testing.T) {
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
		t.Fatalf("expected a spilled, truncated result: %s", out[:200])
	}
	want := filepath.Join(ctx.SessionDir, ToolOutputDir, "call_1.txt")
	if *resp.OutputFile != want {
		t.Fatalf("spill path %q, want %q", *resp.OutputFile, want)
	}
	if !strings.Contains(*resp.Output, want) {
		t.Fatal("the truncation marker must name the full output file")
	}
	full, err := os.ReadFile(want)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for i := 1; i <= 20000; i++ {
		fmt.Fprintf(&b, "%d\n", i)
	}
	if string(full) != b.String() {
		t.Fatalf("spill holds %d bytes, want the whole %d-byte output", len(full), b.Len())
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

func TestSpillCaptureKeepsEveryByteInOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), ToolOutputDir, "x.txt")
	c := newShellOutputCapture(10)
	c.spillTo(path)
	var all strings.Builder
	for _, chunk := range []string{"abc", "defg", "hij", "klmnopqrstuvwxyz", "\x1b[31mred\x1b[0m\r\n", "token sk-ant-api03-" + strings.Repeat("Z", 40) + "\n"} {
		_, _ = c.Write([]byte(chunk))
		all.WriteString(chunk)
	}
	_, truncated := c.result()
	got := c.finishSpill()
	if !truncated || got != path {
		t.Fatalf("truncated=%v path=%q", truncated, got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.HasPrefix(text, "abcdefghijklmnopqrstuvwxyzred\n") {
		t.Fatalf("spill lost order or kept ANSI/CRLF: %q", text)
	}
	if strings.Contains(text, strings.Repeat("Z", 40)) {
		t.Fatal("spilled output must be redacted like the inline output")
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
