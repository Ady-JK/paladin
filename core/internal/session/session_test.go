package session

import (
	"testing"
	"time"

	"github.com/Ady-JK/paladin/core/internal/transcript"
)

func TestHistoryLinksToolResultsToCalls(t *testing.T) {
	calls := []transcript.ToolCall{{ID: "c1", Name: "read", Arguments: `{"path":"workspace/a"}`}}
	msgs := []transcript.Message{
		{Role: transcript.RoleAssistant, Content: "", CreatedAt: time.Now().UTC(), ToolCalls: &calls},
		{Role: transcript.RoleTool, Content: "content", CreatedAt: time.Now().UTC(), ToolCallID: strp("c1"), Name: strp("read")},
	}
	view := HistoryView(msgs)
	if len(view) != 2 {
		t.Fatalf("got %d", len(view))
	}
	if view[1].Arguments == nil || *view[1].Arguments != `{"path":"workspace/a"}` {
		t.Fatalf("got %+v", view[1])
	}
	if view[1].Status == nil || *view[1].Status != "ok" {
		t.Fatalf("got %+v", view[1])
	}
}

func strp(s string) *string { return &s }
