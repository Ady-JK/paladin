package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadsWithOffsetAndLimit(t *testing.T) {
	ctx, ws := testCtx(t)
	if err := os.WriteFile(filepath.Join(ws, "a.txt"), []byte("l1\nl2\nl3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := (ReadTool{}).Call(json.RawMessage(`{"path":"`+filepath.Join(ws, "a.txt")+`","offset":2,"limit":1}`), ctx)
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
	if _, err := (ReadTool{}).Call(json.RawMessage(`{"path":"`+filepath.Join(ws, "b.bin")+`"}`), ctx); err == nil {
		t.Fatal("expected binary rejection")
	}
}

func TestRejectsBareRelativePaths(t *testing.T) {
	ctx, _ := testCtx(t)
	if _, err := (ReadTool{}).Call(json.RawMessage(`{"path":"a.txt"}`), ctx); err == nil {
		t.Fatal("expected rejection")
	}
}

func TestReadRangesAndCaps(t *testing.T) {
	ctx, ws := testCtx(t)
	// Offset past EOF: empty content, start/end 0.
	if err := os.WriteFile(filepath.Join(ws, "a.txt"), []byte("l1\nl2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := (ReadTool{}).Call(json.RawMessage(`{"path":"`+filepath.Join(ws, "a.txt")+`","offset":99}`), ctx)
	if err != nil {
		t.Fatal(err)
	}
	var resp ReadResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Content != "" || resp.HasMore || resp.StartLine != 0 {
		t.Fatalf("got %+v", resp)
	}
	// No trailing newline on last line.
	if err := os.WriteFile(filepath.Join(ws, "b.txt"), []byte("x\ny"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = (ReadTool{}).Call(json.RawMessage(`{"path":"`+filepath.Join(ws, "b.txt")+`"}`), ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Content != "x\ny\n" || resp.EndLine != 2 {
		t.Fatalf("got %+v", resp)
	}
	// Byte cap triggers before line cap: many medium lines exceed 50K.
	var sb strings.Builder
	for i := 0; i < 100; i++ {
		sb.WriteString(strings.Repeat("z", 1000))
		sb.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(ws, "big.txt"), []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = (ReadTool{}).Call(json.RawMessage(`{"path":"`+filepath.Join(ws, "big.txt")+`","limit":2000}`), ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.HasMore || resp.StopReason != "byte_limit" {
		t.Fatalf("got %+v", resp)
	}
	// Long lines are truncated with a marker.
	long := strings.Repeat("a", 3000)
	if err := os.WriteFile(filepath.Join(ws, "long.txt"), []byte(long+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = (ReadTool{}).Call(json.RawMessage(`{"path":"`+filepath.Join(ws, "long.txt")+`"}`), ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(strings.TrimRight(resp.Content, "\n"), "… [truncated]") {
		t.Fatalf("no truncation marker: %q...", resp.Content[:80])
	}
	// Missing file, directory, negative args.
	if _, err := (ReadTool{}).Call(json.RawMessage(`{"path":"`+filepath.Join(ws, "nope.txt")+`"}`), ctx); err == nil {
		t.Fatal("missing accepted")
	}
	if _, err := (ReadTool{}).Call(json.RawMessage(`{"path":"workspace"}`), ctx); err == nil {
		t.Fatal("directory accepted")
	}
	if _, err := (ReadTool{}).Call(json.RawMessage(`{"path":"`+filepath.Join(ws, "a.txt")+`","offset":-1}`), ctx); err == nil {
		t.Fatal("negative offset accepted")
	}
	if _, err := (ReadTool{}).Call(json.RawMessage(`{"path":"`+filepath.Join(ws, "a.txt")+`","limit":-2}`), ctx); err == nil {
		t.Fatal("negative limit accepted")
	}
	if _, err := (ReadTool{}).Call(json.RawMessage(`{bad`), ctx); err == nil {
		t.Fatal("bad json accepted")
	}
	// CRLF normalizes to LF.
	if err := os.WriteFile(filepath.Join(ws, "crlf.txt"), []byte("a\r\nb\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = (ReadTool{}).Call(json.RawMessage(`{"path":"`+filepath.Join(ws, "crlf.txt")+`"}`), ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Content != "a\nb\n" {
		t.Fatalf("got %q", resp.Content)
	}
}

func TestReadRejectsNULBeyondBinaryProbe(t *testing.T) {
	ctx, ws := testCtx(t)
	path := filepath.Join(ws, "late-nul.bin")
	data := append([]byte(strings.Repeat("a", 8*1024+16)), 0, 'x')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := (ReadTool{}).Call(json.RawMessage(`{"path":"`+path+`"}`), ctx)
	if err == nil || !strings.Contains(err.Error(), "binary") {
		t.Fatalf("expected binary-file guidance, got %v", err)
	}
}
