package agent

import (
	"testing"

	"github.com/Ady-JK/paladin/core/internal/inference"
)

// The hub serialises "turn is now in the transcript" with attach
// snapshots, so persist must run entirely inside the gate.
func TestPersistRunsInsideGate(t *testing.T) {
	prov := &mockProvider{streams: []streamScript{{
		chunks: []inference.Chunk{inference.TextChunk("hello")},
	}}}
	a, sess, tx, done := testAgent(t, prov)
	defer close(done)
	calls := 0
	var before, after int
	a.persistGate = func(persist func() error) {
		calls++
		h, _ := sess.History()
		before = len(h)
		_ = persist()
		h, _ = sess.History()
		after = len(h)
	}
	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	drainChunks(tx)
	if calls != 1 {
		t.Fatalf("gate calls = %d, want 1", calls)
	}
	if before != 0 || after == 0 {
		t.Fatalf("persist ran outside the gate: before=%d after=%d", before, after)
	}
}
