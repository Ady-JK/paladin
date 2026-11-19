// reread_test.go covers the identical-read guard: the third back-to-back
// round reading the same files with the same arguments is refused, and
// everything that could make a re-read meaningful resets the streak.

package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/inference"
)

func readCalls(args ...string) []inference.ToolCall {
	calls := make([]inference.ToolCall, len(args))
	for i, a := range args {
		calls[i] = inference.ToolCall{ID: "c", Name: "read", Arguments: a}
	}
	return calls
}

// The third identical round is refused; the first two run.
func TestRereadRefusesThirdIdenticalRound(t *testing.T) {
	g := &rereadGuard{}
	round := readCalls(`{"path":"x.txt"}`)
	if g.noteRound(round) {
		t.Fatal("round 1 must run")
	}
	if g.noteRound(round) {
		t.Fatal("round 2 must run")
	}
	if !g.noteRound(round) {
		t.Fatal("round 3, identical to both before it, must be refused")
	}
}

// Different formatting of the same arguments is still the same call.
func TestRereadTreatsReformattedArgsAsIdentical(t *testing.T) {
	g := &rereadGuard{}
	g.noteRound(readCalls(`{"path":"x.txt"}`))
	g.noteRound(readCalls(`{ "path" : "x.txt" }`))
	if !g.noteRound(readCalls(`{"path":"x.txt"}`)) {
		t.Fatal("whitespace must not make a repeat look new")
	}
}

// A different file, offset or limit is a different call: the streak
// restarts, and reading another slice of the same file never trips the
// guard on its own.
func TestRereadDifferentSliceIsNeverRefused(t *testing.T) {
	g := &rereadGuard{}
	for i := 0; i < 10; i++ {
		args := `{"path":"x.txt","offset":` + string(rune('0'+i)) + `}`
		if g.noteRound(readCalls(args)) {
			t.Fatalf("round with a different offset must never be refused (round %d)", i+1)
		}
	}
	// Interleaving two different files likewise never trips it.
	for i := 0; i < 6; i++ {
		if g.noteRound(readCalls(`{"path":"a.txt"}`)) {
			t.Fatalf("alternating files must never be refused (round %d)", i+1)
		}
		if g.noteRound(readCalls(`{"path":"b.txt"}`)) {
			t.Fatalf("alternating files must never be refused (round %d)", i+1)
		}
	}
}

// Any round that is not all reads resets the streak: after an edit the
// file may have changed, so re-reading it is correct and must run.
func TestRereadEditResetsStreak(t *testing.T) {
	g := &rereadGuard{}
	round := readCalls(`{"path":"x.txt"}`)
	g.noteRound(round)
	g.noteRound(round)
	mixed := []inference.ToolCall{
		{Name: "edit", Arguments: `{"path":"x.txt"}`},
	}
	g.noteRound(mixed)
	if g.noteRound(round) {
		t.Fatal("a read after an edit must run: the file may have changed")
	}
}

// Compaction and a mid-turn user message both end the streak: the
// window changed, so the count no longer refers to what the model sees.
func TestRereadCompactionAndSteerResetStreak(t *testing.T) {
	g := &rereadGuard{}
	round := readCalls(`{"path":"x.txt"}`)
	g.noteRound(round)
	g.noteRound(round)
	g.noteCompaction()
	if g.noteRound(round) {
		t.Fatal("after compaction the earlier result is gone; the read must run")
	}

	g.noteRound(round)
	g.noteRound(round)
	g.noteUserMessage()
	if g.noteRound(round) {
		t.Fatal("after a steer the streak must restart at one")
	}
}

// End to end: a scripted turn that re-reads the same file three times
// gets a refusal as the third round's tool result, the turn continues,
// and the model's text answer still ends it.
func TestRereadRefusalEndsTurnWithAnswer(t *testing.T) {
	a, _, tx, done := testAgent(t, nil)
	t.Cleanup(func() { close(done) })
	drainTx(t, tx)
	// The real read tool runs, so the file must exist: the guard's
	// premise is that the model holds the successful results already.
	path := filepath.Join(a.session.Meta.Cwd, "x.txt")
	if err := os.WriteFile(path, []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}
	args := `{"path":"` + path + `","reason":"checking the file"}`
	prov := &mockProvider{streams: []streamScript{
		callRound("r1", "read", args),
		callRound("r2", "read", args),
		callRound("r3", "read", args),
		textRound("using what I already have"),
	}}
	a.provider = prov

	out, err := a.Run()
	if err != nil {
		t.Fatalf("the refusal must not fail the turn: %v", err)
	}
	if !strings.Contains(out.Reply, "using what I already have") {
		t.Fatalf("turn should end on the model's answer: %+v", out)
	}
	refused := false
	ran := 0
	for _, m := range a.records {
		if m.Role == "tool" {
			if strings.Contains(m.Content, "refused") {
				refused = true
			} else if strings.Contains(m.Content, "x.txt") && !m.IsError {
				ran++
			}
		}
	}
	if !refused {
		t.Fatal("the third identical read must be refused in history")
	}
	if ran != 2 {
		t.Fatalf("the first two reads must have run, got %d", ran)
	}
}
