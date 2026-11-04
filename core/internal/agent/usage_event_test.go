// usage_event_test.go covers the per-call usage signal: every LLM call
// writes the ledger and emits UsageChanged, so the client's spend and
// context fill move without waiting for the turn to end.

package agent

import (
	"testing"

	"github.com/Ady-JK/paladin/core/internal/inference"
)

func TestEveryCallEmitsUsageChanged(t *testing.T) {
	prov := &mockProvider{streams: []streamScript{
		{chunks: []inference.Chunk{
			{ToolCalls: []inference.ToolCall{{ID: "c1", Name: "read", Arguments: `{"path":"x.txt"}`}}},
			{Usage: &inference.Usage{PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12, Cost: 0.01, HasCost: true}},
		}},
		{chunks: []inference.Chunk{
			inference.TextChunk("done"),
			{Usage: &inference.Usage{PromptTokens: 20, CompletionTokens: 3, TotalTokens: 23, Cost: 0.02, HasCost: true}},
		}},
	}}
	a, _, tx, done := testAgent(t, prov)
	t.Cleanup(func() { close(done) })

	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	var signals int
	for _, c := range drainChunks(tx) {
		if c.UsageChanged {
			signals++
		}
	}
	if signals != 2 {
		t.Fatalf("want one usage signal per LLM call (2), got %d", signals)
	}
}

// A call that reports no cost still moves the context fill, so the
// signal must not be tied to a dollar amount.
func TestUsageChangedFiresWithoutCost(t *testing.T) {
	prov := &mockProvider{streams: []streamScript{
		{chunks: []inference.Chunk{
			inference.TextChunk("hi"),
			{Usage: &inference.Usage{PromptTokens: 100, CompletionTokens: 5, TotalTokens: 105}},
		}},
	}}
	a, _, tx, done := testAgent(t, prov)
	t.Cleanup(func() { close(done) })

	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	for _, c := range drainChunks(tx) {
		if c.UsageChanged {
			return
		}
	}
	t.Fatal("a token-only call emitted no usage signal")
}
