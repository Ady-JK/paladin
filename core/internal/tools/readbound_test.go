package tools

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadUpToStopsAtTheLimit(t *testing.T) {
	f := openTemp(t, strings.Repeat("a", 5000))
	got, err := readUpTo(f, 1000)
	if err != nil || len(got) != 1000 {
		t.Fatalf("len=%d err=%v", len(got), err)
	}
	f = openTemp(t, "short")
	if got, err = readUpTo(f, 1000); err != nil || string(got) != "short" {
		t.Fatalf("got %q err=%v", got, err)
	}
}

// A log that grows between the size check and the read must not be read
// past the window: the first max bytes come back, and the file is reported
// as over the limit.
func TestReadJobOutputBoundsAGrowingLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "job.log")
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), 100), 0o600); err != nil {
		t.Fatal(err)
	}
	beforeJobRead = func() {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.Write(bytes.Repeat([]byte("y"), 10_000))
		_ = f.Close()
	}
	t.Cleanup(func() { beforeJobRead = func() {} })
	data, truncated, err := readJobOutput(path, 200)
	if err != nil {
		t.Fatal(err)
	}
	if !truncated || len(data) > 200+len(shellTruncatedMarker) {
		t.Fatalf("truncated=%v len=%d", truncated, len(data))
	}
}

func openTemp(t *testing.T, content string) *os.File {
	t.Helper()
	path := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}
