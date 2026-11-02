package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- P1-8: turn-count staleness ------------------------------------------

func TestNudgeAppearsOnlyAfterReviewTurnsOfSilence(t *testing.T) {
	dir := t.TempDir()
	b := NewBank(t.TempDir())
	b.SetConfig(Config{ReviewTurns: 3})

	if got := b.DueForNudge(dir); got != "" {
		t.Fatalf("nudge on a fresh session: %q", got)
	}
	for i := 1; i <= 2; i++ {
		if _, err := EndTurn(dir); err != nil {
			t.Fatal(err)
		}
		if got := b.DueForNudge(dir); got != "" {
			t.Fatalf("nudge after %d turns: %q", i, got)
		}
	}
	if _, err := EndTurn(dir); err != nil {
		t.Fatal(err)
	}
	got := b.DueForNudge(dir)
	if got == "" {
		t.Fatal("no nudge after the threshold")
	}
	for _, want := range []string{"3 turns", "user", "global", "project", "memory tool"} {
		if !strings.Contains(got, want) {
			t.Fatalf("nudge missing %q:\n%s", want, got)
		}
	}
}

func TestMemoryWriteResetsTheTurnCounter(t *testing.T) {
	dir := t.TempDir()
	b := NewBank(t.TempDir())
	b.SetConfig(Config{ReviewTurns: 2})
	_, _ = EndTurn(dir)
	_, _ = EndTurn(dir)
	if b.DueForNudge(dir) == "" {
		t.Fatal("expected a nudge before the write")
	}
	if err := NoteWrite(dir); err != nil {
		t.Fatal(err)
	}
	st, err := EndTurn(dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.TurnsSinceWrite != 0 {
		t.Fatalf("write did not reset the counter: %+v", st)
	}
	if b.DueForNudge(dir) != "" {
		t.Fatal("nudge survived a memory write")
	}
}

func TestTurnCounterSurvivesReload(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 2; i++ {
		if _, err := EndTurn(dir); err != nil {
			t.Fatal(err)
		}
	}
	if st := LoadReviewState(dir); st.TurnsSinceWrite != 2 {
		t.Fatalf("state = %+v", st)
	}
}

func TestNudgeDisabledByZeroThreshold(t *testing.T) {
	dir := t.TempDir()
	b := NewBank(t.TempDir())
	// normalized() restores the default for zero, so a configured zero is
	// the default rather than "off"; a huge threshold is the off switch.
	b.SetConfig(Config{ReviewTurns: 1 << 20})
	for i := 0; i < 5; i++ {
		_, _ = EndTurn(dir)
	}
	if got := b.DueForNudge(dir); got != "" {
		t.Fatalf("nudge fired under a huge threshold: %q", got)
	}
}

// --- P1-9: background review fork ----------------------------------------

func TestAutoReviewIsOffByDefault(t *testing.T) {
	dir := t.TempDir()
	b := NewBank(t.TempDir())
	if LoadConfigOr(t.TempDir()).AutoReview {
		t.Fatal("autoReview must default to off: a review is a second model request")
	}
	_, _ = EndTurn(dir)
	if b.ShouldForkReview(dir) {
		t.Fatal("forked a review without opting in")
	}
}

func TestAutoReviewForksOncePerUnreviewedTurn(t *testing.T) {
	dir := t.TempDir()
	b := NewBank(t.TempDir())
	b.SetConfig(Config{AutoReview: true})
	if b.ShouldForkReview(dir) {
		t.Fatal("forked before any turn finished")
	}
	if _, err := EndTurn(dir); err != nil {
		t.Fatal(err)
	}
	if !b.ShouldForkReview(dir) {
		t.Fatal("no review after a finished turn")
	}
	if err := MarkReviewed(dir, 1); err != nil {
		t.Fatal(err)
	}
	if b.ShouldForkReview(dir) {
		t.Fatal("reviewed turn forked again")
	}
	if _, err := EndTurn(dir); err != nil {
		t.Fatal(err)
	}
	if !b.ShouldForkReview(dir) {
		t.Fatal("a later turn did not re-arm the review")
	}
}

func TestReviewTaskForbidsWorkspaceWrites(t *testing.T) {
	for _, want := range []string{"memory tool", "cannot change the workspace", "NOTHING", "durable facts"} {
		if !strings.Contains(ReviewTask, want) {
			t.Fatalf("review task missing %q", want)
		}
	}
}

func TestReviewStateIsPerSession(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	_, _ = EndTurn(a)
	_, _ = EndTurn(a)
	_, _ = EndTurn(b)
	if LoadReviewState(a).TurnsSinceWrite != 2 || LoadReviewState(b).TurnsSinceWrite != 1 {
		t.Fatalf("states leaked: a=%+v b=%+v", LoadReviewState(a), LoadReviewState(b))
	}
}

func TestBrokenReviewStateReadsAsZero(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ReviewFile), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if st := LoadReviewState(dir); st.TurnsSinceWrite != 0 || st.ReviewedTurn != 0 {
		t.Fatalf("broken state = %+v", st)
	}
	// A write still works afterwards: a corrupt counter must not wedge
	// the review loop.
	if _, err := EndTurn(dir); err != nil {
		t.Fatal(err)
	}
	if LoadReviewState(dir).TurnsSinceWrite != 1 {
		t.Fatal("counter did not recover")
	}
}
