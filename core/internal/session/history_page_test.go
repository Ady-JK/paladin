package session

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/transcript"
)

// turnStartRows returns the row ordinals where a new turn begins in the
// full view: ordinal 0 and every user row. The renderer groups rows into
// turns the same way (a user row opens one), so a page that starts on one
// of these ordinals never shows orphaned tool calls with no question
// above them.
func turnStartRows(rows []HistoryMessage) []int {
	var starts []int
	for i, row := range rows {
		if i == 0 || row.Role == "user" {
			starts = append(starts, i)
		}
	}
	return starts
}

func TestHistoryPageStatsTurnAligned(t *testing.T) {
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
	rows := HistoryViewStats(msgs, stats)
	total := len(rows)
	starts := turnStartRows(rows)
	for _, limit := range []int{-1, 1, 2, 3, 4, 5, 6, 7, 20} {
		for _, before := range []int{-1, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 99} {
			wantBefore := before
			if wantBefore <= 0 || wantBefore > total {
				wantBefore = total
			}
			effective := limit
			if effective <= 0 {
				effective = 120
			}
			got := HistoryPageStats(msgs, stats, limit, before)
			if got.Total != total {
				t.Fatalf("limit=%d before=%d: total %d, want %d", limit, before, got.Total, total)
			}
			// The window ends exactly at before and begins on a turn
			// boundary — the greatest one at or under the raw slice's
			// start, so paging never widens without bound.
			rawStart := max(0, wantBefore-effective)
			wantStart := 0
			for _, s := range starts {
				if s > rawStart {
					break
				}
				wantStart = s
			}
			if got.Start != wantStart {
				t.Fatalf("limit=%d before=%d: start %d, want %d", limit, before, got.Start, wantStart)
			}
			if len(got.Messages) != wantBefore-wantStart {
				t.Fatalf("limit=%d before=%d: %d rows, want %d", limit, before, len(got.Messages), wantBefore-wantStart)
			}
			// The rows themselves are the full view's [start, before).
			if !reflect.DeepEqual(got.Messages, rows[wantStart:wantBefore]) {
				t.Fatalf("limit=%d before=%d: rows differ from the full view's window", limit, before)
			}
			if got.HasMore != (wantStart > 0) {
				t.Fatalf("limit=%d before=%d: hasMore %v", limit, before, got.HasMore)
			}
		}
	}
}

// The reason the alignment exists: a single turn longer than the limit
// must still arrive whole on attach, question row included.
func TestHistoryPageLongTurnStaysWhole(t *testing.T) {
	callID, name := "call-1", "read"
	long := []transcript.Message{transcript.UserMessage("the question")}
	for i := 0; i < 5; i++ {
		long = append(long,
			transcript.Message{Role: transcript.RoleAssistant, ToolCalls: &[]transcript.ToolCall{{ID: callID, Arguments: `{"path":"a"}`}}},
			transcript.Message{Role: transcript.RoleTool, Content: "result", Name: &name, ToolCallID: &callID},
		)
	}
	long = append(long, transcript.Message{Role: transcript.RoleAssistant, Content: "the answer"})
	page := HistoryPageStats(long, nil, 4, 0)
	if page.Start != 0 || page.HasMore {
		t.Fatalf("long turn page = start %d hasMore %v, want whole", page.Start, page.HasMore)
	}
	if page.Messages[0].Role != "user" || page.Messages[0].Content != "the question" {
		t.Fatalf("long turn page begins with %+v, want the question row", page.Messages[0])
	}
	if got := page.Messages[len(page.Messages)-1]; got.Role != "assistant" || got.Content != "the answer" {
		t.Fatalf("long turn page ends with %+v, want the answer", got)
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
