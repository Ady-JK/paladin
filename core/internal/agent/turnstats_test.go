package agent

import (
	"testing"
	"time"

	"github.com/Ady-JK/paladin/core/internal/transcript"
)

// The stats use generated tokens (completion already counts reasoning)
// and the wall time since the turn started. Stats anchor the turn's last
// row, so continuations can carry them without a user row.
func TestTurnStatsUseGeneratedTokens(t *testing.T) {
	a := &Agent{startedAt: time.Now().Add(-2 * time.Second)}
	a.turnUsage.CompletionTokens = 100
	a.turnUsage.PromptTokens = 999_999

	stats := a.turnStats()
	if stats == nil || stats.Tokens != 100 || stats.DurationMs < 1900 || !stats.Incomplete {
		t.Fatalf("stats = %+v", stats)
	}
	a.phase = phaseDone
	if got := a.turnStats(); got == nil || got.Incomplete {
		t.Fatalf("completed turn stats = %+v", got)
	}
}

// A persisted turn knows where its user row sits, so the renderer can
// place the turn's change card; a resume turn has no user row.
func TestPersistRecordsTheTurnIndex(t *testing.T) {
	dir := t.TempDir()
	sess := transcript.Session{Dir: dir}
	if err := sess.Append(transcript.RoleUser, "older"); err != nil {
		t.Fatal(err)
	}
	a := &Agent{session: sess, startedAt: time.Now()}
	a.history = []transcript.Message{{Role: transcript.RoleUser, Content: "older"}}
	if err := a.persist("new", false); err != nil {
		t.Fatal(err)
	}
	if a.savedIndex == nil || *a.savedIndex != 1 {
		t.Fatalf("index = %v", a.savedIndex)
	}
	a.savedIndex = nil
	if err := a.persist("", true); err != nil {
		t.Fatal(err)
	}
	if a.savedIndex != nil {
		t.Fatalf("resume turn index = %v", a.savedIndex)
	}
	if a.savedStats == nil {
		t.Fatal("resumed turn lost its end-of-turn stats")
	}
}
