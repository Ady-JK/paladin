package transcript

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeWithBadLine leaves a session of two turns with an undecodable
// complete line between them.
func writeWithBadLine(t *testing.T) Session {
	t.Helper()
	store := NewStore(t.TempDir())
	sess, err := store.Create("/p", "prov", "m")
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendTurn([]Message{UserMessage("first"), AssistantMessage("a1")}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sess.Dir, "messages.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("{this is not json}\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if err := sess.AppendTurn([]Message{UserMessage("second"), AssistantMessage("a2")}); err != nil {
		t.Fatal(err)
	}
	return sess
}

// One bad line costs only that line: the readable turns still load.
func TestLoadSkipsAnUndecodableLine(t *testing.T) {
	sess := writeWithBadLine(t)
	msgs, err := sess.History()
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(msgs) != 4 || msgs[2].Content != "second" {
		t.Fatalf("want the 4 readable messages, got %d: %+v", len(msgs), msgs)
	}
	info, err := sessionSummary(sess.Dir)
	if err != nil || info.Count != 4 {
		t.Fatalf("summary = %+v, %v", info, err)
	}
}

// A rewrite from the lenient view would drop the bad line for good, so
// Truncate refuses and leaves the file as it is.
func TestTruncateRefusesToDropAnUndecodableLine(t *testing.T) {
	sess := writeWithBadLine(t)
	path := filepath.Join(sess.Dir, "messages.jsonl")
	before, _ := os.ReadFile(path)
	if err := sess.Truncate(2); err == nil {
		t.Fatal("truncate rewrote a transcript with an undecodable line")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) || !strings.Contains(string(after), "this is not json") {
		t.Fatal("the transcript changed")
	}
}
