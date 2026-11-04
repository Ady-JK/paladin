// nocap_test.go pins the absence of a per-turn tool-call cap. Like
// Codex and OpenCode, a turn runs until the model answers without
// asking for a tool, the user stops it, or a guard (doom loop, denial
// breaker, read-only spiral, compaction) breaks the loop. No fixed
// call count ends a productive turn.

package agent

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/inference"
)

// drainTx keeps the agent's chunk channel flowing while Run executes:
// without a reader the 256-slot buffer fills and the turn deadlocks.
func drainTx(t *testing.T, tx chan ChatChunk) {
	t.Helper()
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	go func() {
		for {
			select {
			case <-tx:
			case <-stop:
				return
			}
		}
	}()
}

// TestLongTurnHasNoToolCallCap runs far past the old 200-call limit and
// must still finish normally. The calls vary, so the doom guard stays
// out of the way, and todo is not read-only, so the search spiral
// guard does not either.
func TestLongTurnHasNoToolCallCap(t *testing.T) {
	prov := &mockProvider{}
	a, _, tx, done := testAgent(t, prov)
	t.Cleanup(func() { close(done) })
	drainTx(t, tx)

	const rounds = 250
	var scripts []streamScript
	for i := 0; i < rounds; i++ {
		args := fmt.Sprintf(`{"items":[{"content":"step %d","status":"pending"}]}`, i)
		scripts = append(scripts, callRound(fmt.Sprintf("c%d", i), "todo", args))
	}
	scripts = append(scripts, textRound("done after many calls"))
	prov.streams = scripts

	out, err := a.Run()
	if err != nil {
		t.Fatalf("a long turn must not be capped: %v (calls=%d)", err, prov.calls)
	}
	if prov.calls != rounds+1 {
		t.Fatalf("want %d rounds, got %d", rounds+1, prov.calls)
	}
	if !strings.Contains(out.Reply, "done after many calls") {
		t.Fatalf("turn should end on the model's answer: %+v", out)
	}
}

// A batch counts each call, not each round: 250 calls in a handful of
// rounds is still one uncapped turn.
func TestNoToolCallCapCountsEveryBatchCall(t *testing.T) {
	prov := &mockProvider{}
	a, _, tx, done := testAgent(t, prov)
	t.Cleanup(func() { close(done) })
	drainTx(t, tx)

	var scripts []streamScript
	for round := 0; round < 5; round++ {
		calls := make([]inference.ToolCall, 0, 60)
		for j := 0; j < 60; j++ {
			id := fmt.Sprintf("c%d-%d", round, j)
			calls = append(calls, inference.ToolCall{
				ID: id, Name: "todo",
				Arguments: fmt.Sprintf(`{"items":[{"content":"%s","status":"pending"}]}`, id),
			})
		}
		scripts = append(scripts, streamScript{chunks: []inference.Chunk{{ToolCalls: calls}}})
	}
	scripts = append(scripts, textRound("batch done"))
	prov.streams = scripts

	out, err := a.Run()
	if err != nil {
		t.Fatalf("300 calls in 5 rounds must not be capped: %v", err)
	}
	if !strings.Contains(out.Reply, "batch done") {
		t.Fatalf("turn should end on the model's answer: %+v", out)
	}
}
