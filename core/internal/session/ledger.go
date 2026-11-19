package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Ady-JK/paladin/core/internal/atomicfile"
)

// ledgerMu serialises usage.json read-modify-writes: a turn and a side
// question can bill the same session at once.
var ledgerMu sync.Mutex

// ErrLedgerCorrupt means usage.json exists but does not decode.
var ErrLedgerCorrupt = errors.New("usage ledger is corrupt")

// ReadLedger loads usage.json. A missing file is zero totals; one that
// does not decode (truncated, empty, garbage) is ErrLedgerCorrupt, never
// zero, so a broken ledger cannot pass for "nothing spent".
func ReadLedger(path string) (UsageTotals, error) {
	var totals UsageTotals
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return totals, nil
	}
	if err != nil {
		return totals, err
	}
	if err := json.Unmarshal(data, &totals); err != nil {
		return UsageTotals{}, fmt.Errorf("%w: %w", ErrLedgerCorrupt, err)
	}
	normalizeEstimated(&totals)
	normalizeInput(&totals)
	return totals, nil
}

// ledgerSchema is the current meaning of the ledger's token counts: a call's
// prompt includes the input served from cache.
const ledgerSchema = 2

// normalizeInput brings a ledger written before schema 2 up to date. Providers
// disagree on what "input" holds: OpenAI-style APIs count cached input in
// prompt_tokens, Anthropic's Messages API leaves it out. Older ledgers recorded
// each as sent, so one session could hold both, and a bucket of 950 input
// tokens beside 155M cache reads read as a 16,000% cache hit and a 0% context.
//
// A bucket whose cache exceeds its prompt can only be of the second kind, so the
// cache is added to its prompt. Days are the finest bucket and are decided one by
// one; the session total follows from them, which keeps a ledger that mixed both
// kinds exact. The hit rate had no per-call record before, so each bucket seeds
// it from its own totals.
func normalizeInput(t *UsageTotals) {
	if t.Schema >= ledgerSchema {
		return
	}
	var dayExtra, dayCalls int64
	var daySum float64
	var dayHits int64
	for _, d := range t.Daily {
		if d == nil {
			continue
		}
		dayExtra += foldCache(d)
		seedCacheHit(d)
		dayCalls += d.Calls
		daySum += d.CacheHitSum
		dayHits += d.CacheHitCalls
	}
	before := t.PromptTokens
	if len(t.Daily) > 0 && dayCalls == t.Calls {
		t.PromptTokens += dayExtra
		t.TotalTokens += dayExtra
		if t.CacheHitCalls == 0 {
			t.CacheHitSum, t.CacheHitCalls = daySum, dayHits
		}
	} else {
		foldCache(&t.Counters)
		seedCacheHit(&t.Counters)
	}
	totalExtra := t.PromptTokens - before

	var modelExtra int64
	var largest *ModelUsage
	for _, m := range t.Models {
		if m == nil {
			continue
		}
		modelExtra += foldCache(&m.Counters)
		seedCacheHit(&m.Counters)
		if largest == nil || m.PromptTokens > largest.PromptTokens {
			largest = m
		}
	}
	// A model's own heuristic cannot see which of its calls came from which day.
	// Whatever it over- or under-counted against the session total is settled on
	// the model that carries most of the traffic.
	if diff := totalExtra - modelExtra; diff != 0 && largest != nil && largest.PromptTokens+diff >= 0 {
		largest.PromptTokens += diff
		largest.TotalTokens += diff
	}
}

// foldCache moves a bucket's cache into its prompt when the bucket kept it
// apart, and returns the tokens added.
func foldCache(c *Counters) int64 {
	cached := c.CacheReadTokens + c.CacheWriteTokens
	if cached <= c.PromptTokens {
		return 0
	}
	c.PromptTokens += cached
	c.TotalTokens += cached
	return cached
}

// seedCacheHit gives a bucket that predates the per-call record a hit rate from
// its own totals, one share for every call it holds.
func seedCacheHit(c *Counters) {
	if c.CacheHitCalls > 0 || c.Calls <= 0 || c.CacheReadTokens <= 0 || c.PromptTokens <= 0 {
		return
	}
	c.CacheHitCalls = c.Calls
	c.CacheHitSum = float64(c.Calls) * CacheShare(c.PromptTokens, c.CacheReadTokens)
}

// normalizeEstimated backfills CostEstimated on a ledger written before
// the field existed. A recorded charge of 0 beside a nonzero list price
// is a subscription or free plan whose tokens were still priced — not
// free tokens — and showing the charge alone would read as $0.
func normalizeEstimated(t *UsageTotals) {
	t.CostEstimated = t.Estimated()
	for _, m := range t.Models {
		if m != nil {
			m.CostEstimated = m.Estimated()
		}
	}
	for _, d := range t.Daily {
		if d != nil {
			d.CostEstimated = d.Estimated()
		}
	}
}

// RecordCall folds one LLM call into the usage.json at path. Billing
// must never fail a turn, so errors are logged, not returned. A corrupt
// ledger is renamed aside (usage.json.corrupt-<unix>) and a new one
// starts with RecoveredFrom naming it, so the lost spend stays visible.
// A ledger that cannot be read at all is left untouched.
func RecordCall(path, sessionID, model string, call Counters) {
	updateLedger(path, sessionID, func(t *UsageTotals) {
		t.AddCall(model, call, time.Now().UTC().Unix())
	})
}

// RecordCallWithContext is RecordCall for a harness that also reports how full
// its own context is.
func RecordCallWithContext(path, sessionID, model string, call Counters, reading ContextReading) {
	updateLedger(path, sessionID, func(t *UsageTotals) {
		t.AddCall(model, call, time.Now().UTC().Unix())
		t.SetContext(model, reading)
	})
}

// RecordContext notes a harness's context report that came with no new call
// (a repeated usage notice), so the gauge still follows it.
func RecordContext(path, sessionID, model string, reading ContextReading) {
	updateLedger(path, sessionID, func(t *UsageTotals) { t.SetContext(model, reading) })
}

func updateLedger(path, sessionID string, apply func(*UsageTotals)) {
	ledgerMu.Lock()
	defer ledgerMu.Unlock()
	totals, err := ReadLedger(path)
	if errors.Is(err, ErrLedgerCorrupt) {
		aside := fmt.Sprintf("%s.corrupt-%d", filepath.Base(path), time.Now().UnixNano())
		if rerr := os.Rename(path, filepath.Join(filepath.Dir(path), aside)); rerr != nil {
			log.Printf("usage: corrupt ledger %s could not be set aside: %v", path, rerr)
			return
		}
		log.Printf("usage: corrupt ledger moved to %s; spend restarts as a lower bound", aside)
		totals = UsageTotals{RecoveredFrom: aside}
	} else if err != nil {
		log.Printf("usage: ledger %s unreadable, call not recorded: %v", path, err)
		return
	}
	totals.SessionID = sessionID
	totals.Schema = ledgerSchema
	apply(&totals)
	data, err := json.MarshalIndent(totals, "", "  ")
	if err == nil {
		err = atomicfile.Write(path, data)
	}
	if err != nil {
		log.Printf("usage: write ledger %s: %v", path, err)
	}
}
