package session

import (
	"testing"
	"time"

	"github.com/Ady-JK/paladin/core/internal/transcript"
)

// A turn's cost lands on the user row that starts it, so the renderer
// can draw one summary per turn.
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
