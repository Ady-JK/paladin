package tools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteThenEditRoundtrip(t *testing.T) {
	dir := t.TempDir()
	ws := filepath.Join(dir, "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	engine := NewEngine()
	if _, err := engine.Write(ws, filepath.Join(ws, "a.txt"), "hello\n", false); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Write(ws, filepath.Join(ws, "a.txt"), "x", false); err == nil {
		t.Fatal("expected exists error")
	} else if ee, ok := err.(*EngineError); !ok || ee.Kind != "exists" {
		t.Fatalf("expected exists, got %v", err)
	}
	res, err := engine.Edit(ws, filepath.Join(ws, "a.txt"), "hello", "bye", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Replacements != 1 {
		t.Fatalf("got %+v", res)
	}
	data, err := os.ReadFile(filepath.Join(ws, "a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "bye\n" {
		t.Fatalf("got %q", data)
	}
}

func TestEditAmbiguousListsOccurrences(t *testing.T) {
	dir := t.TempDir()
	ws := filepath.Join(dir, "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	engine := NewEngine()
	if _, err := engine.Write(ws, filepath.Join(ws, "a.txt"), "x\nx\n", false); err != nil {
		t.Fatal(err)
	}
	_, err := engine.Edit(ws, filepath.Join(ws, "a.txt"), "x", "y", false)
	ee, ok := err.(*EngineError)
	if !ok || ee.Kind != "ambiguous" || ee.Count != 2 {
		t.Fatalf("expected ambiguous count 2, got %v", err)
	}
}

func TestFindOccurrencesContext(t *testing.T) {
	content := "aaa\n\nbbb xxx\n\nccc xxx ddd\n"
	occ := FindOccurrences(content, "xxx")
	if len(occ) != 2 {
		t.Fatalf("got %v", occ)
	}
	if occ[0].StartLine != 3 || occ[1].StartLine != 5 {
		t.Fatalf("got %+v", occ)
	}
	if occ[0].Before == nil || occ[0].Before.Text != "aaa" {
		t.Fatalf("before: %+v", occ[0].Before)
	}
	if occ[0].After == nil || occ[0].After.Text != "ccc xxx ddd" {
		t.Fatalf("after: %+v", occ[0].After)
	}
	if got := CountLines(""); got != 0 {
		t.Fatalf("got %d", got)
	}
	if got := CountLines("a\nb"); got != 2 {
		t.Fatalf("got %d", got)
	}
	if got := CountLines("a\nb\n"); got != 2 {
		t.Fatalf("got %d", got)
	}
}
