// explore_test.go pins the free-exploration guarantee: reading,
// grepping, listing and searching can run for as long as the task
// needs — hundreds of rounds — without any round being refused, on any
// arguments, varied or repeated-with-changes-between. The one narrow
// exception is reread.go's identical back-to-back repeat (see
// reread_test.go). Huge codebases legitimately take a lot of exploring.

package agent

import (
	"fmt"
	"strings"
	"testing"
)

// TestLongExplorationIsNeverRefused runs a turn of far more consecutive
// read-only rounds than the old spiral guard (8) ever allowed, with both
// repeated and varied arguments, and every call must execute: no refusal
// may appear in any tool result.
func TestLongExplorationIsNeverRefused(t *testing.T) {
	prov := &mockProvider{}
	a, _, tx, done := testAgent(t, prov)
	t.Cleanup(func() { close(done) })
	drainTx(t, tx)

	const rounds = 120
	var scripts []streamScript
	for i := 0; i < rounds; i++ {
		// Vary the path so no two calls are identical, like real
		// exploration walking file after file.
		args := fmt.Sprintf(`{"path":"src/file%d.go","reason":"mapping the tree"}`, i)
		scripts = append(scripts, callRound(fmt.Sprintf("c%d", i), "read", args))
	}
	scripts = append(scripts, textRound("explored everything"))
	prov.streams = scripts

	out, err := a.Run()
	if err != nil {
		t.Fatalf("long exploration must not fail the turn: %v", err)
	}
	if prov.calls != rounds+1 {
		t.Fatalf("want %d rounds, got %d", rounds+1, prov.calls)
	}
	if !strings.Contains(out.Reply, "explored everything") {
		t.Fatalf("turn should end on the model's answer: %+v", out)
	}
	for id, content := range toolResultsByID(t, a) {
		if strings.Contains(content, "refused") {
			t.Fatalf("round %s was refused during pure exploration: %s", id, content)
		}
	}
}

// TestIdenticalReadsWithChangeBetweenRuns pins the other half of the
// guarantee: the identical-read guard never refuses a re-read when
// something happened in between that could make the result different.
// A read → edit → read cycle runs every call, however many times it
// repeats, because checking a file after changing it is correct.
func TestIdenticalReadsWithChangeBetweenRuns(t *testing.T) {
	prov := &mockProvider{}
	a, _, tx, done := testAgent(t, prov)
	t.Cleanup(func() { close(done) })
	drainTx(t, tx)
	var scripts []streamScript
	for i := 0; i < 6; i++ {
		scripts = append(scripts,
			callRound("r", "read", `{"path":"x.txt"}`),
			callRound("e", "edit", `{"path":"x.txt"}`),
		)
	}
	scripts = append(scripts, textRound("done"))
	prov.streams = scripts

	if _, err := a.Run(); err != nil {
		t.Fatalf("read-edit cycles must not fail the turn: %v", err)
	}
	for id, content := range toolResultsByID(t, a) {
		if strings.Contains(content, "refused") {
			t.Fatalf("call %s was refused: %s", id, content)
		}
	}
}
