package runtime

import (
	"strings"
	"testing"
	"time"

	"github.com/Ady-JK/paladin/core/internal/agent"
)

func newRecordTurn() *hubTurn { return &hubTurn{toolAt: map[string]int{}} }

// Streaming deltas merge into one replay row without copying the reply so
// far on every chunk (which made long replies quadratic under the hub lock).
func TestRecordMergesDeltasInLinearTime(t *testing.T) {
	turn := newRecordTurn()
	start := time.Now()
	for i := 0; i < 40000; i++ {
		turn.record(TurnEvent{Event: "chat/delta", Seq: uint64(i), Delta: "0123456789abcdef"})
	}
	if took := time.Since(start); took > 500*time.Millisecond {
		t.Fatalf("40k deltas took %v", took)
	}
	log := turn.snapshot()
	if len(log) != 1 || len(log[0].Delta) != 40000*16 || log[0].Seq != 39999 {
		t.Fatalf("merged log: %d rows, %d bytes, seq %d", len(log), len(log[0].Delta), log[0].Seq)
	}
}

// Rows keep their order and content across kinds, and a later read still
// sees text merged after an earlier read.
func TestRecordKeepsRowsApartAcrossKinds(t *testing.T) {
	turn := newRecordTurn()
	turn.record(TurnEvent{Event: "chat/reasoning", Seq: 1, Reasoning: "think "})
	turn.record(TurnEvent{Event: "chat/reasoning", Seq: 2, Reasoning: "more"})
	turn.record(TurnEvent{Event: "chat/delta", Seq: 3, Delta: "hel"})
	if got := turn.snapshot(); len(got) != 2 || got[0].Reasoning != "think more" || got[1].Delta != "hel" {
		t.Fatalf("first read: %+v", got)
	}
	turn.record(TurnEvent{Event: "chat/delta", Seq: 4, Delta: "lo"})
	turn.record(TurnEvent{Event: "chat/tool", Seq: 5, Tool: &agent.ToolActivity{ID: "t1", Name: "read"}})
	turn.record(TurnEvent{Event: "chat/delta", Seq: 6, Delta: " again"})
	got := turn.snapshot()
	var text []string
	for _, e := range got {
		text = append(text, e.Event+":"+e.Delta+e.Reasoning)
	}
	want := "chat/reasoning:think more chat/delta:hello chat/tool: chat/delta: again"
	if strings.Join(text, " ") != want {
		t.Fatalf("rows = %q", strings.Join(text, " "))
	}
}
