package session

import (
	"testing"

	"github.com/Ady-JK/paladin/core/internal/transcript"
)

func TestIncompleteHistoryCrossesSteeringAndPageBoundaries(t *testing.T) {
	msgs := []transcript.Message{
		transcript.UserMessage("check"),
		{Role: transcript.RoleAssistant, Content: "working"},
		transcript.UserMessage("steer"),
		{Role: transcript.RoleAssistant, Content: "partial"},
	}
	stats := map[int]*transcript.TurnStats{3: {DurationMs: 2000, Incomplete: true}}
	// The older page ends before the steering message and has no stats row.
	rows := historyViewRange(msgs, stats, 0, 2)
	if len(rows) != 2 || !rows[0].Incomplete || !rows[1].Incomplete || rows[1].Stats != nil {
		t.Fatalf("older page lost interrupted outcome: %+v", rows)
	}
	msgs = append(msgs, transcript.UserMessage("next"), transcript.Message{Role: transcript.RoleAssistant, Content: "finished"})
	stats[5] = &transcript.TurnStats{DurationMs: 1000}
	rows = HistoryViewStats(msgs, stats)
	if len(rows) != 6 || !rows[3].Incomplete || rows[4].Incomplete || rows[5].Incomplete {
		t.Fatalf("outcome leaked across turn boundary: %+v", rows)
	}
}
