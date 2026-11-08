package transcript

import (
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
