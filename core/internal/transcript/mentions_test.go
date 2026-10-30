package transcript

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMentionsFindsNeedlesAcrossReadBoundaries(t *testing.T) {
	dir := t.TempDir()
	// Put one needle exactly across the 64 KiB read boundary.
	body := strings.Repeat("x", 64*1024-4) + "abc#123" + strings.Repeat("y", 100) + "tail#9\n"
	if err := os.WriteFile(filepath.Join(dir, "messages.jsonl"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Session{Dir: dir}.Mentions([]string{"abc#123", "tail#9", "absent#1"})
	if err != nil {
		t.Fatal(err)
	}
	if !got["abc#123"] || !got["tail#9"] || got["absent#1"] {
		t.Fatalf("got %v", got)
	}
	if got, err := (Session{Dir: t.TempDir()}).Mentions([]string{"a"}); err != nil || len(got) != 0 {
		t.Fatalf("missing transcript: %v %v", got, err)
	}
}
