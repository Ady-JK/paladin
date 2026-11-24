package session

import (
	"testing"
	"time"

	"github.com/Ady-JK/paladin/core/internal/transcript"
)

// The supplied anchor is retained, including for legacy callers that
// provide a user-row index.
func TestHistoryViewStatsAttachToTheTurnStart(t *testing.T) {
	msgs := []transcript.Message{
		{Role: transcript.RoleUser, Content: "q1", CreatedAt: time.Now().UTC()},
		{Role: transcript.RoleAssistant, Content: "a1", CreatedAt: time.Now().UTC()},
		{Role: transcript.RoleUser, Content: "q2", CreatedAt: time.Now().UTC()},
		{Role: transcript.RoleAssistant, Content: "a2", CreatedAt: time.Now().UTC()},
	}
	view := HistoryViewStats(msgs, map[int]*transcript.TurnStats{
		0: {DurationMs: 65_000, Tokens: 1_234},
	})
	if len(view) != 4 {
		t.Fatalf("rows = %d", len(view))
	}
	if view[0].Stats == nil || view[0].Stats.DurationMs != 65_000 || view[0].Stats.Tokens != 1_234 {
		t.Fatalf("first turn stats = %+v", view[0].Stats)
	}
	if view[2].Stats != nil {
		t.Fatalf("second turn stats = %+v", view[2].Stats)
	}
}

func TestHistoryStatsCloseAfterReasoningAndAnswer(t *testing.T) {
	stats := &transcript.TurnStats{DurationMs: 2000, Incomplete: true}
	rows := HistoryViewStats([]transcript.Message{{
		Role: transcript.RoleAssistant, Content: "partial answer", Reasoning: "thinking",
	}}, map[int]*transcript.TurnStats{0: stats})
	if len(rows) != 2 || rows[0].Stats != nil || rows[1].Stats != stats || !rows[1].Stats.Incomplete {
		t.Fatalf("stats must close the answer after reasoning: %+v", rows)
	}
	rows = HistoryViewStats([]transcript.Message{{
		Role: transcript.RoleAssistant, Reasoning: "thinking only",
	}}, map[int]*transcript.TurnStats{0: stats})
	if len(rows) != 1 || rows[0].Stats != stats {
		t.Fatalf("thinking-only turn lost its boundary: %+v", rows)
	}
}
