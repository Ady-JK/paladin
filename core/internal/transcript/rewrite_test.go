package transcript

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRewriteMessagesChangesOnlyWhatTheCallbackTouches(t *testing.T) {
	store := NewStore(t.TempDir())
	sess, err := store.Create(t.TempDir(), "codex", "m")
	if err != nil {
		t.Fatal(err)
	}
	id := "exec-1"
	name := "shell"
	if err := sess.AppendJournalTurn([]Message{
		UserMessage("run it"),
		{Role: RoleAssistant, ToolCalls: &[]ToolCall{{ID: id, Name: name, Arguments: "{}"}}},
		{Role: RoleTool, ToolCallID: &id, Name: &name, Content: `{"status":"running"}`},
	}, &TurnStats{DurationMs: 7, Tokens: 3}, "journal-1"); err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendTurn([]Message{UserMessage("next"), {Role: RoleAssistant, Content: "ok"}}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(sess.Dir, "messages.jsonl"))
	lines := strings.Split(strings.TrimSpace(string(before)), "\n")

	n, err := sess.RewriteMessages(func(m *Message) bool {
		if m.Role == RoleTool && m.ToolCallID != nil && *m.ToolCallID == id {
			m.Content = `{"status":"completed","exit_code":0}`
			return true
		}
		return false
	})
	if err != nil || n != 1 {
		t.Fatalf("rewrote %d, %v", n, err)
	}
	after, _ := os.ReadFile(filepath.Join(sess.Dir, "messages.jsonl"))
	got := strings.Split(strings.TrimSpace(string(after)), "\n")
	if len(got) != 2 || got[1] != lines[1] {
		t.Fatalf("the untouched turn changed:\n%s", after)
	}
	if !strings.Contains(got[0], `completed`) || !strings.Contains(got[0], `"journalId":"journal-1"`) || !strings.Contains(got[0], `"durationMs":7`) {
		t.Fatalf("the touched turn lost its journal id or stats: %s", got[0])
	}
	history, _ := sess.History()
	if len(history) != 5 || history[2].Content != `{"status":"completed","exit_code":0}` {
		t.Fatalf("history %+v", history)
	}
	// Nothing to change: the file is left alone.
	stat, _ := os.Stat(filepath.Join(sess.Dir, "messages.jsonl"))
	if n, err := sess.RewriteMessages(func(*Message) bool { return false }); err != nil || n != 0 {
		t.Fatalf("no-op rewrote %d, %v", n, err)
	}
	if again, _ := os.Stat(filepath.Join(sess.Dir, "messages.jsonl")); !again.ModTime().Equal(stat.ModTime()) {
		t.Fatal("a rewrite that changed nothing still replaced the file")
	}
}
