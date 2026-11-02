// toollimit_test.go covers the per-turn tool-call failsafe: a model
// stuck issuing ever-varying calls (which the identical-call doomGuard
// cannot see) ends the turn with a resumable error instead of looping
// forever.

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

func TestToolCallBudgetEndsRunawayTurn(t *testing.T) {
	prov := &mockProvider{}
	a, _, tx, done := testAgent(t, prov)
	t.Cleanup(func() { close(done) })
	drainTx(t, tx)
	// maxToolCallsPerTurn single-call rounds, then one more: the loop
	// must stop at the budget, not consume the extra script.
	var scripts []streamScript
	for i := 0; i < maxToolCallsPerTurn+1; i++ {
		scripts = append(scripts, callRound(fmt.Sprintf("c%d", i), "read", `{"path":"x.txt"}`))
	}
	prov.streams = scripts
	_, err := a.Run()
	if err == nil || !strings.Contains(err.Error(), "tool call budget exhausted") {
		t.Fatalf("want budget error, got %v (calls=%d)", err, prov.calls)
	}
	if prov.calls != maxToolCallsPerTurn+1 {
		t.Fatalf("want %d rounds, got %d", maxToolCallsPerTurn+1, prov.calls)
	}
}

func TestToolCallBudgetCountsBatches(t *testing.T) {
	prov := &mockProvider{}
	a, _, tx, done := testAgent(t, prov)
	t.Cleanup(func() { close(done) })
	drainTx(t, tx)
	// Ten rounds of 30 parallel calls: the 7th round crosses 200.
	var scripts []streamScript
	for i := 0; i < 10; i++ {
		var chunks []inference.Chunk
		var calls []inference.ToolCall
		for j := 0; j < 30; j++ {
			calls = append(calls, inference.ToolCall{ID: fmt.Sprintf("c%d-%d", i, j), Name: "read", Arguments: `{"path":"x.txt"}`})
		}
		chunks = append(chunks, inference.Chunk{ToolCalls: calls})
		scripts = append(scripts, streamScript{chunks: chunks})
	}
	prov.streams = scripts
	_, err := a.Run()
	if err == nil || !strings.Contains(err.Error(), "tool call budget exhausted") {
		t.Fatalf("want budget error, got %v (calls=%d)", err, prov.calls)
	}
	if prov.calls != 7 {
		t.Fatalf("want 7 rounds, got %d", prov.calls)
	}
}

func TestToolCallBudgetLeavesValidHistory(t *testing.T) {
	prov := &mockProvider{}
	a, sess, _, done := testAgent(t, prov)
	t.Cleanup(func() { close(done) })
	prov.streams = []streamScript{
		callRound("c1", "read", `{"path":"x.txt"}`),
	}
	// Force the budget to trip on the very next round by pre-filling
	// the counter: history repair, not the mock, is under test.
	a.toolCalls = maxToolCallsPerTurn
	prov.streams = append(prov.streams, callRound("c2", "read", `{"path":"y.txt"}`))
	_, err := a.Run()
	if err == nil || !strings.Contains(err.Error(), "tool call budget exhausted") {
		t.Fatalf("want budget error, got %v", err)
	}
	msgs, herr := sess.History()
	if herr != nil {
		t.Fatal(herr)
	}
	// The refused round's call is paired with an aborted result, so a
	// later turn can load this transcript without repair errors.
	var calls, results int
	for _, m := range msgs {
		if m.Role == "assistant" && m.ToolCalls != nil {
			calls += len(*m.ToolCalls)
		}
		if m.Role == "tool" {
			results++
		}
	}
	if calls == 0 || results < calls {
		t.Fatalf("calls=%d results=%d in %+v", calls, results, msgs)
	}
}
