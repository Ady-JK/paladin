package main

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func collectLines(t *testing.T, input string, max int) (lines []string, over []string, err error) {
	t.Helper()
	err = readLines(strings.NewReader(input), max,
		func(line []byte) { lines = append(lines, string(line)) },
		func(prefix []byte) { over = append(over, string(prefix)) })
	return lines, over, err
}

func TestReadLinesSplitsAndKeepsAnUnterminatedTail(t *testing.T) {
	lines, over, err := collectLines(t, "a\nbb\n\nccc", 100)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a", "bb", "", "ccc"}
	if len(lines) != len(want) || len(over) != 0 {
		t.Fatalf("lines=%q over=%q", lines, over)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Fatalf("line %d = %q, want %q", i, lines[i], want[i])
		}
	}
}

func TestReadLinesSkipsAnOversizedLineAndKeepsGoing(t *testing.T) {
	big := `{"id":7,"method":"x","params":"` + strings.Repeat("z", 5000) + `"}`
	lines, over, err := collectLines(t, "first\n"+big+"\nlast\n", 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 || lines[0] != "first" || lines[1] != "last" {
		t.Fatalf("lines = %q", lines)
	}
	if len(over) != 1 || !strings.HasPrefix(over[0], `{"id":7,`) || len(over[0]) > 4096 {
		t.Fatalf("oversized prefix = %q", over)
	}
}

func TestReadLinesAcceptsALineExactlyAtTheLimit(t *testing.T) {
	line := strings.Repeat("x", 1000)
	lines, over, err := collectLines(t, line+"\n", 1000)
	if err != nil || len(lines) != 1 || len(over) != 0 {
		t.Fatalf("lines=%d over=%d err=%v", len(lines), len(over), err)
	}
}

func TestReadLinesDropsAnOversizedTailWithoutANewline(t *testing.T) {
	lines, over, err := collectLines(t, "ok\n"+strings.Repeat("y", 5000), 1000)
	if err != nil || len(lines) != 1 || len(over) != 1 {
		t.Fatalf("lines=%q over=%d err=%v", lines, len(over), err)
	}
}

type failingReader struct{ n int }

func (f *failingReader) Read(p []byte) (int, error) {
	if f.n > 0 {
		f.n--
		return copy(p, "x\n"), nil
	}
	return 0, errors.New("pipe broke")
}

func TestReadLinesReportsReadErrors(t *testing.T) {
	var got int
	err := readLines(&failingReader{n: 2}, 100, func([]byte) { got++ }, func([]byte) {})
	if err == nil || errors.Is(err, io.EOF) || got != 2 {
		t.Fatalf("got=%d err=%v", got, err)
	}
}

func TestRequestIDFromPrefix(t *testing.T) {
	cases := map[string]string{
		`{"id":7,"method":"x"`:        `7`,
		`{"method":"x","id":"m12",`:   `"m12"`,
		`{"id" : "a b" ,"params":{}}`: `"a b"`,
		`{"method":"x"}`:              ``,
		`{"id":{"nested":1}}`:         ``,
	}
	for in, want := range cases {
		if got := string(requestIDFromPrefix([]byte(in))); got != want {
			t.Errorf("%s -> %q, want %q", in, got, want)
		}
	}
}
