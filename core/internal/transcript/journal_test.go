package transcript

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInterruptedJournalRecoversToolCallsWithoutRepeatingThem(t *testing.T) {
	store := NewStore(t.TempDir())
	s, err := store.Create(t.TempDir(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	j, err := s.BeginJournal([]Message{UserMessage("make a file")})
	if err != nil {
		t.Fatal(err)
	}
	calls := []ToolCall{{ID: "c1", Name: "shell", Arguments: `{"cmd":"write"}`}}
	if err := j.Checkpoint([]Message{UserMessage("make a file"), {Role: RoleAssistant, ToolCalls: &calls}}, ""); err != nil {
		t.Fatal(err)
	}
	if recovered, err := s.RecoverInterrupted(); err != nil || recovered {
		t.Fatalf("recovered active journal: %v %v", recovered, err)
	}
	j.Release()
	if recovered, err := s.RecoverInterrupted(); err != nil || !recovered {
		t.Fatalf("recovery: %v %v", recovered, err)
	}
	msgs, err := s.History()
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 4 || msgs[2].Role != RoleTool || !msgs[2].IsError || !strings.Contains(msgs[2].Content, "may have") || !strings.Contains(msgs[3].Content, "interrupted") {
		t.Fatalf("recovered messages: %+v", msgs)
	}
	if recovered, err := s.RecoverInterrupted(); err != nil || recovered {
		t.Fatalf("duplicate recovery: %v %v", recovered, err)
	}
}

func TestCommittedJournalIsNotRecoveredTwice(t *testing.T) {
	store := NewStore(t.TempDir())
	s, err := store.Create(t.TempDir(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	j, err := s.BeginJournal([]Message{UserMessage("hello")})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AppendJournalTurn([]Message{UserMessage("hello"), AssistantMessage("done")}, nil, j.ID); err != nil {
		t.Fatal(err)
	}
	j.Release()
	if recovered, err := s.RecoverInterrupted(); err != nil || recovered {
		t.Fatalf("committed turn recovered: %v %v", recovered, err)
	}
	msgs, err := s.History()
	if err != nil || len(msgs) != 2 {
		t.Fatalf("duplicate transcript: %+v %v", msgs, err)
	}
}

func TestRecoveryPreservesCompletedResultsAndPartialReply(t *testing.T) {
	store := NewStore(t.TempDir())
	s, err := store.Create(t.TempDir(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	callID, name := "done", "shell"
	calls := []ToolCall{{ID: callID, Name: name, Arguments: "{}"}}
	msgs := []Message{UserMessage("work"), {Role: RoleAssistant, ToolCalls: &calls}, {Role: RoleTool, ToolCallID: &callID, Name: &name, Content: "completed"}}
	j, err := s.BeginJournal(msgs)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Checkpoint(msgs, "partial answer"); err != nil {
		t.Fatal(err)
	}
	j.Release()
	if ok, err := s.RecoverInterrupted(); err != nil || !ok {
		t.Fatalf("recover: %v %v", ok, err)
	}
	got, err := s.History()
	if err != nil || len(got) != 5 || got[2].Content != "completed" || got[3].Content != "partial answer" {
		t.Fatalf("recovered: %+v %v", got, err)
	}
}

func journalFiles(t *testing.T, s Session) (header, msgs, partial string) {
	t.Helper()
	read := func(name string) string {
		b, _ := os.ReadFile(filepath.Join(s.Dir, name))
		return string(b)
	}
	return read("active-turn.json"), read("active-turn.msgs"), read("active-turn.partial")
}

// A checkpoint writes only what changed, so a long turn costs its growth,
// not its whole length, at every tool call and every second of streaming.
func TestCheckpointAppendsOnlyWhatChanged(t *testing.T) {
	store := NewStore(t.TempDir())
	s, _ := store.Create(t.TempDir(), "", "")
	msgs := []Message{UserMessage("go")}
	j, err := s.BeginJournal(msgs)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Release()
	header0, _, _ := journalFiles(t, s)
	var lastSize int
	for i := 0; i < 20; i++ {
		msgs = append(msgs, AssistantMessage(strings.Repeat("x", 1000)))
		if err := j.Checkpoint(msgs, ""); err != nil {
			t.Fatal(err)
		}
		_, body, _ := journalFiles(t, s)
		if i > 0 && len(body)-lastSize > 1500 {
			t.Fatalf("checkpoint %d grew the journal by %d bytes", i, len(body)-lastSize)
		}
		lastSize = len(body)
	}
	header1, _, _ := journalFiles(t, s)
	if header0 != header1 {
		t.Fatal("the header is rewritten on every checkpoint")
	}
	// An unchanged checkpoint writes nothing.
	_, before, _ := journalFiles(t, s)
	if err := j.Checkpoint(msgs, ""); err != nil {
		t.Fatal(err)
	}
	if _, after, _ := journalFiles(t, s); after != before {
		t.Fatal("an unchanged checkpoint changed the journal")
	}
}

// A message that changes or moves after it was written is replaced, not
// duplicated: recovery sees exactly the last checkpoint.
func TestCheckpointReplacesAChangedMiddle(t *testing.T) {
	store := NewStore(t.TempDir())
	s, _ := store.Create(t.TempDir(), "", "")
	a, b, c := UserMessage("one"), AssistantMessage("two"), AssistantMessage("three")
	j, _ := s.BeginJournal([]Message{a})
	if err := j.Checkpoint([]Message{a, b, c}, ""); err != nil {
		t.Fatal(err)
	}
	b2 := AssistantMessage("two, revised")
	if err := j.Checkpoint([]Message{a, b2}, "typing"); err != nil {
		t.Fatal(err)
	}
	j.Release()
	if ok, err := s.RecoverInterrupted(); err != nil || !ok {
		t.Fatalf("recover: %v %v", ok, err)
	}
	got, _ := s.History()
	var texts []string
	for _, m := range got {
		texts = append(texts, m.Content)
	}
	if len(got) != 4 || texts[0] != "one" || texts[1] != "two, revised" || texts[2] != "typing" || !strings.Contains(texts[3], "interrupted") {
		t.Fatalf("recovered %q", texts)
	}
	if _, body, partial := journalFiles(t, s); body != "" || partial != "" {
		t.Fatalf("recovery left journal files behind: %q %q", body, partial)
	}
}

// A journal an older version left behind (one file holding everything)
// still recovers.
func TestRecoveryReadsTheOlderSingleFileJournal(t *testing.T) {
	store := NewStore(t.TempDir())
	s, _ := store.Create(t.TempDir(), "", "")
	old := `{"id":"legacy-1","startSize":0,"messages":[{"role":"user","content":"old turn"}],"partial":"half an answer"}`
	if err := os.WriteFile(filepath.Join(s.Dir, "active-turn.json"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.RecoverInterrupted(); err != nil || !ok {
		t.Fatalf("recover: %v %v", ok, err)
	}
	got, _ := s.History()
	if len(got) != 3 || got[0].Content != "old turn" || got[1].Content != "half an answer" {
		t.Fatalf("recovered %+v", got)
	}
}

// A torn last line (the process died mid-append) is dropped, the rest kept.
func TestRecoveryDropsATornJournalLine(t *testing.T) {
	store := NewStore(t.TempDir())
	s, _ := store.Create(t.TempDir(), "", "")
	j, _ := s.BeginJournal([]Message{UserMessage("go")})
	if err := j.Checkpoint([]Message{UserMessage("go"), AssistantMessage("kept")}, ""); err != nil {
		t.Fatal(err)
	}
	j.Release()
	f, _ := os.OpenFile(filepath.Join(s.Dir, "active-turn.msgs"), os.O_APPEND|os.O_WRONLY, 0o600)
	_, _ = f.WriteString(`{"role":"assistant","content":"tor`)
	_ = f.Close()
	if ok, err := s.RecoverInterrupted(); err != nil || !ok {
		t.Fatalf("recover: %v %v", ok, err)
	}
	got, _ := s.History()
	if len(got) != 3 || got[1].Content != "kept" {
		t.Fatalf("recovered %+v", got)
	}
}
