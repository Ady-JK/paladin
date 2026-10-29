package session

import (
	"testing"
	"time"

	"github.com/Ady-JK/paladin/core/internal/transcript"
)

func TestHistoryViewShowsCompactionDividerAndHidesReplays(t *testing.T) {
	now := time.Now().UTC()
	calls := []transcript.ToolCall{{ID: "c1", Name: "read", Arguments: `{}`}}
	msgs := []transcript.Message{
		{Role: transcript.RoleUser, Content: "task", CreatedAt: now},
		{Role: transcript.RoleAssistant, Content: "", CreatedAt: now, ToolCalls: &calls},
		{Role: transcript.RoleTool, Content: "r", CreatedAt: now, ToolCallID: strp("c1"), Name: strp("read")},
		{Role: transcript.RoleSystem, Content: "SUMMARY", CreatedAt: now, Compaction: &transcript.Compaction{TokensBefore: 9000, Reason: "overflow"}},
		{Role: transcript.RoleAssistant, Content: "", CreatedAt: now, ToolCalls: &calls, Replay: true},
		{Role: transcript.RoleTool, Content: "r", CreatedAt: now, ToolCallID: strp("c1"), Name: strp("read"), Replay: true},
		{Role: transcript.RoleAssistant, Content: "done", CreatedAt: now},
	}
	view := HistoryView(msgs)
	if len(view) != 5 {
		t.Fatalf("got %d rows: %+v", len(view), view)
	}
	div := view[3]
	if div.Role != "system" || div.Kind == nil || *div.Kind != "compaction" || div.Content != "SUMMARY" {
		t.Fatalf("divider %+v", div)
	}
	if view[4].Content != "done" {
		t.Fatalf("tail %+v", view[4])
	}
}
