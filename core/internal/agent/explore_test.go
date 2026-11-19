// explore_test.go pins the guarantee the doom loop and the search-spiral
// guard used to break: reading, grepping, listing and searching can run
// for as long as the task needs — hundreds of rounds — without any
// round being refused, on any arguments, identical or varied. Huge
// codebases legitimately take a lot of exploring.

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

// TestIdenticalReadsAreNotRefused pins the other half of the guarantee:
// even the exact same read, called back to back many times, runs every
// time. The old doom guard refused the third identical call; a model
// comparing a file before and after a write, or one that simply wants
// the content again after compaction trimmed it away, must get it.
func TestIdenticalReadsAreNotRefused(t *testing.T) {
	prov := &mockProvider{streams: []streamScript{
		callRound("c1", "read", `{"path":"x.txt"}`),
		callRound("c2", "read", `{"path":"x.txt"}`),
		callRound("c3", "read", `{"path":"x.txt"}`),
		callRound("c4", "read", `{"path":"x.txt"}`),
		textRound("done re-reading"),
	}}
	a, _, _, done := testAgent(t, prov)
	defer close(done)

	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	results := toolResultsByID(t, a)
	if len(results) != 4 {
		t.Fatalf("all four identical reads must run, got %d results", len(results))
	}
	for id, content := range results {
		if strings.Contains(content, "refused") {
			t.Fatalf("identical read %s was refused: %s", id, content)
		}
	}
}
