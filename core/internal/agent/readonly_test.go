// readonly_test.go covers the search/read-only spiral guard: many
// consecutive rounds that only search or read get the next such round
// refused, while any round that acts resets the streak.

package agent

import (
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/inference"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

func readCall(id string) inference.ToolCall {
	return inference.ToolCall{ID: id, Name: "read", Arguments: `{"path":"x.txt"}`}
}

func TestReadOnlyStreakRefusesAtLimit(t *testing.T) {
	a := &Agent{}
	for i := 0; i < readOnlyRoundLimit-1; i++ {
		if a.noteReadOnlyRound([]inference.ToolCall{readCall("c")}) {
			t.Fatalf("round %d refused too early", i+1)
		}
	}
	if !a.noteReadOnlyRound([]inference.ToolCall{readCall("c")}) {
		t.Fatalf("round %d must be refused", readOnlyRoundLimit)
	}
}

func TestReadOnlyStreakResetsOnAction(t *testing.T) {
	a := &Agent{}
	for i := 0; i < readOnlyRoundLimit-1; i++ {
		a.noteReadOnlyRound([]inference.ToolCall{readCall("c")})
	}
	write := inference.ToolCall{ID: "w", Name: "write", Arguments: `{}`}
	if a.noteReadOnlyRound([]inference.ToolCall{readCall("c"), write}) {
		t.Fatal("a round that acts must reset, not refuse")
	}
	if a.noteReadOnlyRound([]inference.ToolCall{readCall("c")}) {
		t.Fatal("streak restarted, must not refuse yet")
	}
}

func TestReadOnlyRefusalEndsSpiral(t *testing.T) {
	prov := &mockProvider{}
	a, sess, tx, done := testAgent(t, prov)
	t.Cleanup(func() { close(done) })
	drainTx(t, tx)
	var scripts []streamScript
	for i := 0; i < readOnlyRoundLimit; i++ {
		scripts = append(scripts, callRound("r", "read", `{"path":"x.txt"}`))
	}
	scripts = append(scripts, textRound("here is what I found"))
	prov.streams = scripts
	out, err := a.Run()
	if err != nil {
		t.Fatalf("refusal must not fail the turn: %v", err)
	}
	if prov.calls != readOnlyRoundLimit+1 {
		t.Fatalf("want %d rounds, got %d", readOnlyRoundLimit+1, prov.calls)
	}
	if !strings.Contains(out.Reply, "here is what I found") {
		t.Fatalf("turn should end on the text answer: %+v", out)
	}
	msgs, err := sess.History()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range msgs {
		if m.Role == transcript.RoleTool && strings.Contains(m.Content, "read-only round") {
			found = true
		}
	}
	if !found {
		t.Fatal("refused round must be recorded in history")
	}
}
