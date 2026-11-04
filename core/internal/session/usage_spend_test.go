package session

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCountersSpendUsdSubscription covers the ledger side of the $0
// bug: a subscription session's Cost is 0 while ListCost carries what
// the tokens would have cost. SpendUsd must report the latter.
func TestCountersSpendUsdSubscription(t *testing.T) {
	c := Counters{Cost: 0, ListCost: 0.16143655, HasCost: true, CostEstimated: true}
	if got, want := c.SpendUsd(), 0.16143655; got != want {
		t.Fatalf("SpendUsd = %v, want the list price %v", got, want)
	}
}

// TestCountersSpendUsdBilled: a real charge is reported as it is.
func TestCountersSpendUsdBilled(t *testing.T) {
	c := Counters{Cost: 0.25, ListCost: 0.5, HasCost: true}
	if got := c.SpendUsd(); got != 0.25 {
		t.Fatalf("SpendUsd = %v, want the 0.25 charged", got)
	}
}

// TestCountersAddKeepsEstimated: folding a subscription call into a
// session keeps the marker, so the tally stays an estimate.
func TestCountersAddKeepsEstimated(t *testing.T) {
	var c Counters
	c.Add(Counters{Cost: 0, ListCost: 0.02, HasCost: true, CostEstimated: true})
	c.Add(Counters{Cost: 0, ListCost: 0.03, HasCost: true, CostEstimated: true})
	if !c.CostEstimated {
		t.Fatal("the estimate marker was lost when folding counters")
	}
	if got, want := c.SpendUsd(), 0.05; got != want {
		t.Fatalf("SpendUsd = %v, want %v", got, want)
	}
}

// TestUsageTotalsSubscriptionLedger round-trips the exact kind of
// usage.json that showed the bug, proving the read path keeps the flag.
func TestUsageTotalsSubscriptionLedger(t *testing.T) {
	const raw = `{"sessionId":"s","model":"deepseek-v4.1-flash","calls":164,` +
		`"promptTokens":18042705,"completionTokens":69216,"totalTokens":18111921,` +
		`"cost":0,"listCost":0.16143655,"hasCost":true,"costEstimated":true}`
	path := filepath.Join(t.TempDir(), "usage.json")
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	totals, err := ReadLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	if totals.SpendUsd() == 0 {
		t.Fatal("a ledger with 18M tokens and a list price reported no spend")
	}
	if got, want := totals.SpendUsd(), 0.16143655; got != want {
		t.Fatalf("SpendUsd = %v, want %v", got, want)
	}
}

// TestLegacySubscriptionLedgerReadsAsEstimate covers the ledgers written
// before costEstimated existed (the ones on disk from 0.8.31 and
// earlier). Their numbers alone say "charged 0 beside a list price",
// which must still show the list price instead of a misleading $0.
func TestLegacySubscriptionLedgerReadsAsEstimate(t *testing.T) {
	const raw = `{"sessionId":"s","model":"deepseek-v4.1-flash","calls":55,` +
		`"promptTokens":3271142,"completionTokens":17981,"reasoningTokens":4892,` +
		`"cacheReadTokens":3194112,"totalTokens":3289123,` +
		`"cost":0,"listCost":0.06385086,"hasCost":true,` +
		`"models":{"deepseek-v4.1-flash":{"calls":55,"promptTokens":3271142,` +
		`"completionTokens":17981,"totalTokens":3289123,"cost":0,` +
		`"listCost":0.06385086,"hasCost":true}},` +
		`"daily":{"2026-11-04":{"calls":55,"totalTokens":3289123,"cost":0,` +
		`"listCost":0.06385086,"hasCost":true}}}`
	path := filepath.Join(t.TempDir(), "usage.json")
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	totals, err := ReadLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	if !totals.Estimated() || !totals.CostEstimated {
		t.Fatal("a legacy subscription ledger must read as an estimate")
	}
	if got, want := totals.SpendUsd(), 0.06385086; got != want {
		t.Fatalf("SpendUsd = %v, want the list price %v", got, want)
	}
	view := totals.View("s", 200_000, true, nil)
	if !view.CostEstimated || view.SpendUsd() == 0 {
		t.Fatalf("view = %+v, want an estimated list price", view.Counters)
	}
	if len(view.Models) != 1 || !view.Models[0].CostEstimated {
		t.Fatalf("model view = %+v, want an estimated list price", view.Models)
	}
	if len(view.Daily) != 1 || !view.Daily[0].CostEstimated {
		t.Fatalf("daily view = %+v, want an estimated list price", view.Daily)
	}
}

// TestCountersAddInfersLegacyEstimate: folding counters that predate the
// marker must infer it too, so fleet rollups do not sum $0.
func TestCountersAddInfersLegacyEstimate(t *testing.T) {
	var c Counters
	c.Add(Counters{Cost: 0, ListCost: 0.02, HasCost: true})
	if !c.CostEstimated {
		t.Fatal("a priced-but-unbilled call lost its estimate when folded")
	}
	if got, want := c.SpendUsd(), 0.02; got != want {
		t.Fatalf("SpendUsd = %v, want %v", got, want)
	}
}
