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
