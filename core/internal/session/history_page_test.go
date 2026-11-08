package session

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/transcript"
)

// referencePageHistory is the original full-render-and-slice behavior.
func referencePageHistory(rows []HistoryMessage, limit, before int) HistoryPage {
	if limit <= 0 {
		limit = 120
	}
	total := len(rows)
	if before <= 0 || before > total {
		before = total
	}
	start := max(0, before-limit)
	if rows == nil {
		rows = []HistoryMessage{}
	}
	return HistoryPage{Messages: rows[start:before], Start: start, HasMore: start > 0, Total: total}
}

func TestHistoryPageStatsMatchesFullView(t *testing.T) {
	callID, name := "call-1", "read"
	msgs := []transcript.Message{
		transcript.UserMessage("first"),
		{Role: transcript.RoleAssistant, Content: "answer", Reasoning: "thinking", ToolCalls: &[]transcript.ToolCall{{ID: callID, Arguments: `{"path":"a"}`}}},
		{Role: transcript.RoleTool, Content: "result", Name: &name, ToolCallID: &callID},
		{Role: transcript.RoleAssistant, ToolCalls: &[]transcript.ToolCall{{ID: callID, Arguments: `{"path":"later"}`}}},
		{Role: transcript.RoleTool, Content: "reused result", Name: &name, ToolCallID: &callID},
		{Role: transcript.RoleAssistant, Content: "", Reasoning: "reasoning only"},
		{Role: transcript.RoleAssistant, Content: ""},
		{Role: transcript.RoleUser, Content: "replayed", Replay: true},
		{Role: transcript.RoleSystem, Content: "summary", Compaction: &transcript.Compaction{}},
		transcript.UserMessage("last"),
	}
	stats := map[int]*transcript.TurnStats{1: {Tokens: 20}, 9: {Tokens: 10}}
	for _, limit := range []int{-1, 1, 2, 3, 4, 5, 6, 7, 20} {
		for _, before := range []int{-1, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 99} {
			want := referencePageHistory(HistoryViewStats(msgs, stats), limit, before)
			got := HistoryPageStats(msgs, stats, limit, before)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("limit=%d before=%d: got %+v, want %+v", limit, before, got, want)
			}
		}
	}
}

func BenchmarkHistoryPageManyToolCalls(b *testing.B) {
	msgs := make([]transcript.Message, 0, 20000)
	for i := 0; i < 10000; i++ {
		id := fmt.Sprintf("call-%d", i)
		calls := []transcript.ToolCall{{ID: id, Arguments: `{"path":"file"}`}}
		msgs = append(msgs, transcript.Message{Role: transcript.RoleAssistant, ToolCalls: &calls}, transcript.Message{Role: transcript.RoleTool, ToolCallID: &id, Content: "result"})
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = HistoryPageStats(msgs, nil, 120, 0)
	}
}

func BenchmarkHistoryPageLongTranscript(b *testing.B) {
	msgs := make([]transcript.Message, 0, 20000)
	for i := 0; i < 10000; i++ {
		msgs = append(msgs, transcript.UserMessage(fmt.Sprintf("question %d", i)), transcript.Message{Role: transcript.RoleAssistant, Content: "answer"})
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = HistoryPageStats(msgs, nil, 120, 0)
	}
}
