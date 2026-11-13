package session

import "testing"

func TestUnpricedCallsMarkSpendIncomplete(t *testing.T) {
	var totals UsageTotals
	totals.AddCall("luna", Counters{HasCost: true, CostEstimated: true, ListCost: 0.01}, 1)
	totals.AddCall("other", Counters{PromptTokens: 100}, 2)
	if totals.UnpricedCalls != 1 || !totals.SpendIncomplete() {
		t.Fatalf("totals = %+v", totals)
	}
}

func TestMixedEstimatedAndChargedSpend(t *testing.T) {
	var totals UsageTotals
	totals.AddCall("copilot", Counters{HasCost: true, CostEstimated: true, ListCost: 0.1}, 1)
	totals.AddCall("proxy", Counters{HasCost: true, Cost: 0.2, ListCost: 0.3}, 2)
	if totals.SpendUsd() < 0.299999 || totals.SpendUsd() > 0.300001 {
		t.Fatalf("mixed spend = %v", totals.SpendUsd())
	}
}
