package session

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

// The cache hit rate is the average of every call's own hit share, not
// total cache reads over total input: one huge cached call must not hide ten
// cold ones.
func TestCacheHitIsAveragedPerCall(t *testing.T) {
	var totals UsageTotals
	totals.AddCall("m", Counters{PromptTokens: 100_000, CacheReadTokens: 90_000, CacheHitCalls: 1, CacheHitSum: 0.9}, 100)
	totals.AddCall("m", Counters{PromptTokens: 1_000, CacheHitCalls: 1}, 101)
	if totals.CacheHitCalls != 2 || !near(totals.CacheHitSum, 0.9) {
		t.Fatalf("session: %+v", totals.Counters)
	}
	m := totals.Models["m"]
	if m.CacheHitCalls != 2 || !near(m.CacheHitSum, 0.9) {
		t.Fatalf("model: %+v", m.Counters)
	}
	if d := totals.Daily[dayKey(100)]; d.CacheHitCalls != 2 || !near(d.CacheHitSum, 0.9) {
		t.Fatalf("day: %+v", d)
	}
}

// A caller that only filled in the token counts still gets its share counted,
// when the call read from the cache.
func TestCacheHitIsDerivedWhenACallReportsOnlyTokens(t *testing.T) {
	var totals UsageTotals
	totals.AddCall("m", Counters{PromptTokens: 200, CacheReadTokens: 50}, 100)
	if totals.CacheHitCalls != 1 || !near(totals.CacheHitSum, 0.25) {
		t.Fatalf("got %+v", totals.Counters)
	}
	// A call that says nothing about the cache is not counted as a miss.
	totals.AddCall("m", Counters{PromptTokens: 200}, 101)
	if totals.CacheHitCalls != 1 {
		t.Fatalf("an unreported call counted: %+v", totals.Counters)
	}
}

func TestCacheShareIsClamped(t *testing.T) {
	if CacheShare(0, 5) != 0 || CacheShare(100, -1) != 0 || CacheShare(100, 500) != 1 || !near(CacheShare(200, 50), 0.25) {
		t.Fatal("share not clamped to 0..1")
	}
}

// The ledger of a real Copilot session that mixed both conventions: the first
// day came through chat completions (cache counted inside the prompt), the
// second through the Messages API (input_tokens left the cache out), so a
// total of 6.3M "input" sat beside 161M cache reads and read as 2544% hits.
const mixedLedger = `{
  "sessionId": "s", "model": "claude-sonnet-5.5",
  "calls": 278, "promptTokens": 6361462, "completionTokens": 350926,
  "cacheReadTokens": 161830524, "cacheWriteTokens": 5010049, "totalTokens": 6712388,
  "cost": 0, "listCost": 32.7, "hasCost": true, "costEstimated": true,
  "lastInputTokens": 4, "updatedAt": 1794634071,
  "models": {
    "claude-sonnet-5.5": {"calls": 266, "promptTokens": 5966692, "completionTokens": 343638, "cacheReadTokens": 161510332, "cacheWriteTokens": 5010049, "totalTokens": 6310330, "lastInputTokens": 4},
    "gemini-3.8-flash": {"calls": 12, "promptTokens": 394770, "completionTokens": 7288, "cacheReadTokens": 320192, "totalTokens": 402058, "lastInputTokens": 42709}
  },
  "daily": {
    "2026-11-13": {"calls": 50, "promptTokens": 6360512, "completionTokens": 100000, "cacheReadTokens": 6033631, "totalTokens": 6460512},
    "2026-11-14": {"calls": 228, "promptTokens": 950, "completionTokens": 250926, "cacheReadTokens": 155796893, "cacheWriteTokens": 5010049, "totalTokens": 251876}
  }
}`

func readMixed(t *testing.T) UsageTotals {
	t.Helper()
	path := filepath.Join(t.TempDir(), "usage.json")
	if err := os.WriteFile(path, []byte(mixedLedger), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestLegacyLedgerCountsCacheInsidePrompt(t *testing.T) {
	got := readMixed(t)
	const extra = int64(155796893 + 5010049) // only the Messages-API day lacked the cache
	if want := int64(6361462) + extra; got.PromptTokens != want {
		t.Fatalf("total prompt = %d, want %d", got.PromptTokens, want)
	}
	if want := int64(6712388) + extra; got.TotalTokens != want {
		t.Fatalf("total tokens = %d, want %d", got.TotalTokens, want)
	}
	if d := got.Daily["2026-11-13"]; d.PromptTokens != 6360512 {
		t.Fatalf("a day that already counted its cache was changed: %+v", d)
	}
	if d := got.Daily["2026-11-14"]; d.PromptTokens != 950+extra || d.TotalTokens != 251876+extra {
		t.Fatalf("messages-api day = %+v", d)
	}
	claude, gemini := got.Models["claude-sonnet-5.5"], got.Models["gemini-3.8-flash"]
	if claude.PromptTokens != 5966692+extra {
		t.Fatalf("claude prompt = %d", claude.PromptTokens)
	}
	if gemini.PromptTokens != 394770 {
		t.Fatalf("a model that already counted its cache was changed: %d", gemini.PromptTokens)
	}
	// No bucket may claim more cache than input any more.
	for name, c := range map[string]Counters{"total": got.Counters, "claude": claude.Counters, "gemini": gemini.Counters} {
		if c.CacheReadTokens+c.CacheWriteTokens > c.PromptTokens {
			t.Errorf("%s: cache %d+%d exceeds prompt %d", name, c.CacheReadTokens, c.CacheWriteTokens, c.PromptTokens)
		}
	}
}

func TestLegacyLedgerSeedsAverageCacheHit(t *testing.T) {
	got := readMixed(t)
	if got.CacheHitCalls != 278 {
		t.Fatalf("calls with cache data = %d", got.CacheHitCalls)
	}
	rate := got.CacheHitSum / float64(got.CacheHitCalls) * 100
	if rate < 90 || rate > 100 {
		t.Fatalf("cache hit = %.1f%%, want a sane share", rate)
	}
}

func TestMigratedLedgerIsNotMigratedTwice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	if err := os.WriteFile(path, []byte(mixedLedger), 0o600); err != nil {
		t.Fatal(err)
	}
	RecordCall(path, "s", "claude-sonnet-5.5", Counters{PromptTokens: 600_000, CacheReadTokens: 590_000, CacheWriteTokens: 4_000, CompletionTokens: 10, TotalTokens: 600_010, CacheHitCalls: 1, CacheHitSum: 590.0 / 600})
	first, err := ReadLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	RecordCall(path, "s", "claude-sonnet-5.5", Counters{PromptTokens: 10, TotalTokens: 10})
	second, err := ReadLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	if second.PromptTokens != first.PromptTokens+10 {
		t.Fatalf("second read migrated again: %d -> %d", first.PromptTokens, second.PromptTokens)
	}
	if first.LastInputTokens != 600_000 {
		t.Fatalf("last input = %d", first.LastInputTokens)
	}
}
