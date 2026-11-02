package errlog

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendWritesRawError(t *testing.T) {
	dir := t.TempDir()
	Append(dir, "turn", errors.New("provider unavailable: stream: unexpected end of JSON input"))
	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	line := string(data)
	if !strings.Contains(line, "\tturn\t") {
		t.Fatalf("source missing: %q", line)
	}
	if !strings.Contains(line, "provider unavailable: stream: unexpected end of JSON input") {
		t.Fatalf("error text not raw: %q", line)
	}
	if !strings.HasSuffix(line, "\n") {
		t.Fatalf("missing newline: %q", line)
	}
	// UTC timestamp first, so logs sort.
	if !strings.HasPrefix(line, "20") || !strings.Contains(line[0:strings.IndexByte(line, '\t')], "T") {
		t.Fatalf("timestamp missing: %q", line)
	}
}

func TestAppendKeepsMultiLineRaw(t *testing.T) {
	dir := t.TempDir()
	AppendText(dir, "panic", "boom\nstack line 1\nstack line 2")
	data, _ := os.ReadFile(filepath.Join(dir, FileName))
	if !strings.Contains(string(data), "boom\nstack line 1\nstack line 2") {
		t.Fatalf("stack trace was filtered: %q", data)
	}
}

func TestAppendAppendsInOrder(t *testing.T) {
	dir := t.TempDir()
	AppendText(dir, "one", "first")
	AppendText(dir, "two", "second")
	data, _ := os.ReadFile(filepath.Join(dir, FileName))
	if strings.Count(string(data), "\n") != 2 {
		t.Fatalf("want two lines: %q", data)
	}
	if strings.Index(string(data), "first") > strings.Index(string(data), "second") {
		t.Fatalf("out of order: %q", data)
	}
}

func TestAppendIgnoresNilAndEmpty(t *testing.T) {
	dir := t.TempDir()
	Append(dir, "turn", nil)
	AppendText("", "turn", "no dir")
	AppendText(dir, "turn", "   ")
	if _, err := os.Stat(filepath.Join(dir, FileName)); !os.IsNotExist(err) {
		t.Fatalf("nothing should have been written: %v", err)
	}
}

func TestAppendNeverFailsOnMissingDir(t *testing.T) {
	// A deleted session folder must not make the caller panic or hang.
	Append(filepath.Join(t.TempDir(), "gone"), "turn", errors.New("x"))
}

func TestRotateKeepsOnePreviousFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	if err := os.WriteFile(path, make([]byte, maxBytes), 0o600); err != nil {
		t.Fatal(err)
	}
	AppendText(dir, "turn", "after rotation")
	rotated, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatalf("previous log missing: %v", err)
	}
	if len(rotated) != maxBytes {
		t.Fatalf("rotated size %d", len(rotated))
	}
	fresh, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(fresh), "after rotation") || strings.Contains(string(fresh), "\x00") {
		t.Fatalf("fresh log wrong: %q", string(fresh))
	}
}

func TestGlobalWritesUnderLogs(t *testing.T) {
	dir := t.TempDir()
	Global(dir, "rpc:openSession", "not found")
	data, err := os.ReadFile(filepath.Join(dir, "logs", "errors.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "rpc:openSession\tnot found") {
		t.Fatalf("got %q", data)
	}
}
