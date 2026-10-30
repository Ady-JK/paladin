package agent

import (
	"os"
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/inference"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

func costly(script streamScript, usd float64) streamScript {
	script.chunks = append(script.chunks, inference.Chunk{Usage: &inference.Usage{TotalTokens: 10, Cost: usd, HasCost: true}})
	return script
}

func budgetChunks(chunks []ChatChunk) []BudgetNotice {
	var out []BudgetNotice
	for _, c := range chunks {
		if c.Budget != nil {
			out = append(out, *c.Budget)
		}
	}
	return out
}

func TestBudgetReachedWrapsUpWithoutTools(t *testing.T) {
	prov := &mockProvider{streams: []streamScript{
		costly(callRound("c1", "read", `{"path":"/nope"}`), 0.6),
		{chunks: []inference.Chunk{
			inference.TextChunk("done: read; left: tests"),
			{ToolCalls: []inference.ToolCall{{ID: "c2", Name: "read", Arguments: `{"path":"/nope"}`}}},
		}},
	}}
	a, sess, tx, done := testAgent(t, prov)
	defer close(done)
	a.budget = func() float64 { return 0.5 }
	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	if prov.calls != 2 {
		t.Fatalf("want one wrap-up call after the budget is hit, got %d calls", prov.calls)
	}
	if len(prov.streamDefs[1]) != 0 {
		t.Fatalf("wrap-up call must have no tools, got %v", prov.streamDefs[1])
	}
	if !strings.Contains(lastUser(prov.streamMsgs[1]), "budget") {
		t.Fatalf("wrap-up call must ask for a summary: %q", lastUser(prov.streamMsgs[1]))
	}
	got := budgetChunks(drainChunks(tx))
	if len(got) != 1 || got[0].State != BudgetReached || got[0].Limit != 0.5 || got[0].Spent < 0.6 || got[0].Ended {
		t.Fatalf("budget notices %+v", got)
	}
	h, _ := sess.History()
	last := h[len(h)-1]
	if last.Role != transcript.RoleAssistant || last.Content != "done: read; left: tests" {
		t.Fatalf("summary must end the transcript: %+v", last)
	}
	for _, m := range h {
		if m.Role == transcript.RoleUser && strings.Contains(m.Content, "budget") {
			t.Fatalf("the wrap-up prompt is not a user message: %+v", m)
		}
	}
}

func TestBudgetAlreadySpentEndsTurnWithoutCallingModel(t *testing.T) {
	prov := &mockProvider{streams: []streamScript{{chunks: []inference.Chunk{inference.TextChunk("x")}}}}
	a, sess, tx, done := testAgent(t, prov)
	defer close(done)
	if err := os.WriteFile(sess.UsagePath(), []byte(`{"cost":2,"hasCost":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	a.budget = func() float64 { return 1 }
	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	if prov.calls != 0 {
		t.Fatalf("an exhausted budget must not call the model, got %d calls", prov.calls)
	}
	got := budgetChunks(drainChunks(tx))
	if len(got) != 1 || got[0].State != BudgetReached || got[0].Spent != 2 || got[0].Limit != 1 || !got[0].Ended {
		t.Fatalf("budget notices %+v", got)
	}
}

func TestBudgetWarnsOnceWhenCostIsUnknown(t *testing.T) {
	noCost := inference.Chunk{Usage: &inference.Usage{TotalTokens: 10}}
	prov := &mockProvider{streams: []streamScript{
		{chunks: append(callRound("c1", "read", `{"path":"/nope"}`).chunks, noCost)},
		{chunks: append(callRound("c2", "read", `{"path":"/nope2"}`).chunks, noCost)},
		{chunks: []inference.Chunk{inference.TextChunk("ok"), noCost}},
	}}
	a, _, tx, done := testAgent(t, prov)
	defer close(done)
	a.budget = func() float64 { return 0.01 }
	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	if prov.calls != 3 {
		t.Fatalf("unknown cost must not stop the turn: %d calls", prov.calls)
	}
	got := budgetChunks(drainChunks(tx))
	if len(got) != 1 || got[0].State != BudgetUnknownCost {
		t.Fatalf("want one unknownCost warning, got %+v", got)
	}
}

func TestBudgetWrapUpLeavesSteeringForLater(t *testing.T) {
	inbox := &steerInbox{}
	prov := &mockProvider{streams: []streamScript{
		costly(callRound("c1", "read", `{"path":"/nope"}`), 1),
		{chunks: []inference.Chunk{inference.TextChunk("summary")}},
	}}
	prov.afterChunks = func(idx int) {
		inbox.pending = append(inbox.pending, SteerMessage{Message: "keep going"})
	}
	a, _, _, done := testAgent(t, prov)
	defer close(done)
	a.steer = inbox.take
	a.budget = func() float64 { return 0.5 }
	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	if prov.calls != 2 || len(inbox.pending) == 0 {
		t.Fatalf("steers must wait for a later turn: calls=%d pending=%v", prov.calls, inbox.pending)
	}
}

func TestNoBudgetMeansNoNotices(t *testing.T) {
	prov := &mockProvider{streams: []streamScript{
		costly(callRound("c1", "read", `{"path":"/nope"}`), 5),
		{chunks: []inference.Chunk{inference.TextChunk("ok")}},
	}}
	a, _, tx, done := testAgent(t, prov)
	defer close(done)
	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	if got := budgetChunks(drainChunks(tx)); len(got) != 0 || prov.calls != 2 {
		t.Fatalf("no budget set: notices=%+v calls=%d", got, prov.calls)
	}
}
