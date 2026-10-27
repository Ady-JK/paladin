package session

import "testing"

func TestUsageTotalsAdd(t *testing.T) {
	var totals UsageTotals
	totals.AddCall("m", 10, 5, 1, 2, 3, 15, 0.01, 0.02, true, 100)
	totals.AddCall("m", 4, 1, 0, 0, 0, 5, 0, 0, false, 101)
	if totals.Calls != 2 || totals.PromptTokens != 14 || totals.CompletionTokens != 6 ||
		totals.ReasoningTokens != 1 || totals.CacheReadTokens != 2 || totals.CacheWriteTokens != 3 ||
		totals.TotalTokens != 20 || totals.UpdatedAt != 101 {
		t.Fatalf("got %+v", totals)
	}
	if !totals.HasCost || totals.Cost != 0.01 || totals.ListCost != 0.02 {
		t.Fatalf("cost %+v", totals)
	}
	// A cost-less call keeps the earlier spend.
	if totals.Model != "m" {
		t.Fatalf("model %q", totals.Model)
	}
}

func TestUsageViewFillPercent(t *testing.T) {
	var totals UsageTotals
	totals.AddCall("m", 100, 100, 0, 0, 0, 200, 0.05, 0.1, true, 1)
	view := totals.View("s", 1000, true)
	if view.SessionID != "s" || !view.ContextKnown || view.ContextWindow != 1000 {
		t.Fatalf("got %+v", view)
	}
	if view.FillPercent == nil || *view.FillPercent != 20 {
		t.Fatalf("fill %+v", view.FillPercent)
	}
	if view.TotalTokens != 200 || !view.HasCost || view.Cost != 0.05 {
		t.Fatalf("got %+v", view)
	}
	// Unknown window: no percentage, tokens still visible.
	unknown := totals.View("s", 200000, false)
	if unknown.FillPercent != nil || unknown.ContextKnown {
		t.Fatalf("got %+v", unknown)
	}
	if unknown.TotalTokens != 200 {
		t.Fatalf("got %+v", unknown)
	}
}
