package agent

import (
	"testing"
	"time"

	"github.com/Ady-JK/paladin/core/internal/transcript"
)

// The stats use generated tokens (completion already counts reasoning)
// and the wall time since the turn started; a resume turn has no user
// row to attach a summary to.
func TestTurnStatsUseGeneratedTokens(t *testing.T) {
	a := &Agent{startedAt: time.Now().Add(-2 * time.Second)}
	a.turnUsage.CompletionTokens = 100
	a.turnUsage.PromptTokens = 999_999

	stats := a.turnStats(false)
	if stats == nil || stats.Tokens != 100 || stats.DurationMs < 1900 {
		t.Fatalf("stats = %+v", stats)
	}
	if got := a.turnStats(true); got != nil {
		t.Fatalf("resume turn got stats: %+v", got)
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
}
