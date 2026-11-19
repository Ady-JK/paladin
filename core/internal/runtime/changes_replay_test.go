package runtime

import (
	"testing"

	"github.com/Ady-JK/paladin/core/internal/transcript"
)

// A compaction re-records the kept tail after its summary, so a turn's
// tool calls can appear twice. The copy must not change which turn the
// call belongs to, or make a read-only turn look like one that edits files.
func TestReplayedCopiesDoNotCreditTheirTurnWithEdits(t *testing.T) {
	writeCall := func(id string) *[]transcript.ToolCall {
		calls := []transcript.ToolCall{{ID: id, Name: "write", Arguments: `{"path":"a.txt"}`}}
		return &calls
	}
	readCall := func(id string) *[]transcript.ToolCall {
		calls := []transcript.ToolCall{{ID: id, Name: "read", Arguments: `{"path":"a.txt"}`}}
		return &calls
	}
	w1, r1 := "w1", "r1"
	msgs := []transcript.Message{
		{Role: transcript.RoleUser, Content: "edit it"},
		{Role: transcript.RoleAssistant, ToolCalls: writeCall("w1")},
		{Role: transcript.RoleTool, ToolCallID: &w1},
		{Role: transcript.RoleUser, Content: "just read"},
		{Role: transcript.RoleAssistant, ToolCalls: readCall("r1")},
		{Role: transcript.RoleTool, ToolCallID: &r1},
		{Role: transcript.RoleSystem, Content: "summary", Compaction: &transcript.Compaction{}},
		// Replayed copies of the kept tail, after the summary.
		{Role: transcript.RoleUser, Content: "just read", Replay: true},
		{Role: transcript.RoleAssistant, ToolCalls: writeCall("w1"), Replay: true},
		{Role: transcript.RoleTool, ToolCallID: &w1, Replay: true},
	}
	turns := []TurnSnapshot{{Turn: 0}, {Turn: 3}}

	if got := turnCanChangeFiles(msgs, turns); got[0] != true || got[3] != false {
		t.Fatalf("turnCanChangeFiles = %v, want the edit turn only", got)
	}
	ids := turnCallIDs(msgs, turns)
	if len(ids[0]) != 1 || ids[0][0] != "w1" {
		t.Fatalf("edit turn call ids = %v, want [w1] once", ids[0])
	}
	if len(ids[3]) != 1 || ids[3][0] != "r1" {
		t.Fatalf("read turn call ids = %v, want [r1] only", ids[3])
	}
}
