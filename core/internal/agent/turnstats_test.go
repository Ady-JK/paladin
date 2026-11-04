package agent

import (
	"testing"
	"time"
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
