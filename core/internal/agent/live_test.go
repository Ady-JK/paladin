package agent

import (
	"testing"

	"github.com/Ady-JK/paladin/core/internal/inference"
)

func TestLiveSnapshotShowsToolCallsWhileTheyRun(t *testing.T) {
	prov := &mockProvider{streams: []streamScript{callRound("c1", "read", `{"path":"x.txt"}`), textRound("ok")}}
	a, _, _, done := testAgent(t, prov)
	defer close(done)
	var snaps [][]inference.LlmMessage
	a.live = func(m []inference.LlmMessage) { snaps = append(snaps, m) }
	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	// Before round 1, once c1 is called (still running), once c1 has a
	// result, and before round 2.
	found := false
	for _, snap := range snaps {
		tail := snap[len(snap)-1]
		if tail.Role == "assistant" && len(tail.ToolCalls) == 1 && tail.ToolCalls[0].ID == "c1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no snapshot showed the in-flight call: %d snapshots", len(snaps))
	}
}

func TestLiveSnapshotIsPublishedBeforeEveryModelRound(t *testing.T) {
	prov := &mockProvider{streams: []streamScript{callRound("c1", "read", `{"path":"x.txt"}`), textRound("ok")}}
	a, _, _, done := testAgent(t, prov)
	defer close(done)
	var snaps [][]inference.LlmMessage
	a.live = func(m []inference.LlmMessage) { snaps = append(snaps, m) }
	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	if len(snaps) < 2 {
		t.Fatalf("want a snapshot per round, got %d", len(snaps))
	}
	last := snaps[len(snaps)-1]
	if tail := last[len(last)-1]; tail.Role != "tool" || tail.ToolCallID == nil || *tail.ToolCallID != "c1" {
		t.Fatalf("second round's snapshot should end with c1's result: %+v", tail)
	}
	// Snapshots are copies: later rounds must not rewrite them.
	if len(snaps[0]) >= len(last) {
		t.Fatalf("first snapshot grew: %d vs %d", len(snaps[0]), len(last))
	}
}
