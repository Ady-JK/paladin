package session

import "testing"

func TestUsageTotalsAdd(t *testing.T) {
	var totals UsageTotals
	totals.AddCall("m", 10, 5, 1, 2, 3, 15, 0.01, 0.02, true, 100)
	totals.AddCall("m", 4, 1, 0, 0, 0, 5, 0, 0, false, 101)
	if totals.LastInputTokens != 4 {
		t.Fatalf("last input %+v", totals)
	}
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
	// A later, smaller call moves the context reading even though the
	// cumulative total only grows: fill is last-call input / window.
	totals.AddCall("m", 50, 10, 0, 0, 0, 60, 0, 0, false, 2)
	view := totals.View("s", 1000, true, func(string) (int64, bool) { return 1000, true })
	if view.SessionID != "s" || !view.ContextKnown || view.ContextWindow != 1000 {
		t.Fatalf("got %+v", view)
	}
	if view.LastInputTokens != 50 {
		t.Fatalf("last input %+v", view)
	}
	if view.FillPercent == nil || *view.FillPercent != 5 {
		t.Fatalf("fill %+v", view.FillPercent)
	}
	if view.TotalTokens != 260 || !view.HasCost || view.Cost != 0.05 {
		t.Fatalf("got %+v", view)
	}
	// Unknown window: no percentage, tokens still visible.
	unknown := totals.View("s", 200000, false, nil)
	if unknown.FillPercent != nil || unknown.ContextKnown {
		t.Fatalf("got %+v", unknown)
	}
	if unknown.TotalTokens != 260 {
		t.Fatalf("got %+v", unknown)
	}
}

func TestUsageTotalsPerModel(t *testing.T) {
	var totals UsageTotals
	totals.AddCall("b", 10, 5, 0, 0, 0, 15, 0.01, 0.02, true, 1)
	totals.AddCall("a", 100, 100, 4, 1, 2, 200, 0.05, 0.1, true, 2)
	totals.AddCall("a", 50, 50, 0, 0, 0, 100, 0, 0, false, 3)
	if totals.Calls != 3 || totals.TotalTokens != 315 || totals.PromptTokens != 160 {
		t.Fatalf("totals %+v", totals)
	}
	if !totals.HasCost || !closeTo(totals.Cost, 0.06) {
		t.Fatalf("cost %+v", totals)
	}
	a := totals.Models["a"]
	if a == nil || a.Calls != 2 || a.TotalTokens != 300 || a.ReasoningTokens != 4 {
		t.Fatalf("model a %+v", totals.Models)
	}
	if a.LastInputTokens != 50 || totals.LastInputTokens != 50 {
		t.Fatalf("last input a=%+v totals=%+v", a, totals)
	}
	if !a.HasCost || !closeTo(a.Cost, 0.05) {
		t.Fatalf("model a cost %+v", a)
	}
	b := totals.Models["b"]
	if b == nil || b.Calls != 1 || b.TotalTokens != 15 {
		t.Fatalf("model b %+v", totals.Models)
	}
	// Empty model still bills the session but keeps no per-model book.
	totals.AddCall("", 1, 0, 0, 0, 0, 1, 0, 0, false, 4)
	if totals.TotalTokens != 316 || len(totals.Models) != 2 {
		t.Fatalf("got %+v", totals)
	}
}

func TestUsageViewModelsSortedWithWindows(t *testing.T) {
	var totals UsageTotals
	totals.AddCall("b", 10, 5, 0, 0, 0, 15, 0.01, 0.02, true, 1)
	totals.AddCall("a", 100, 100, 0, 0, 0, 200, 0, 0, false, 2)
	windows := map[string]int64{"a": 1000, "b": 100}
	view := totals.View("s", 1000, true, func(m string) (int64, bool) {
		w, ok := windows[m]
		return w, ok
	})
	if len(view.Models) != 2 {
		t.Fatalf("got %+v", view.Models)
	}
	// Heaviest first: a (200) before b (15).
	if view.Models[0].Model != "a" || view.Models[1].Model != "b" {
		t.Fatalf("order %+v", view.Models)
	}
	am, bm := view.Models[0], view.Models[1]
	// a's single call took 100 input tokens (200 total): 100/1000 = 10%.
	if !am.ContextKnown || am.ContextWindow != 1000 || am.FillPercent == nil || *am.FillPercent != 10 {
		t.Fatalf("model a %+v", am)
	}
	if am.LastInputTokens != 100 {
		t.Fatalf("model a last input %+v", am)
	}
	if am.HasCost {
		t.Fatalf("model a should not report cost: %+v", am)
	}
	// b's single call took 10 input tokens (15 total): 10/100 = 10%.
	if !bm.ContextKnown || bm.FillPercent == nil || *bm.FillPercent != 10 {
		t.Fatalf("model b %+v", bm)
	}
	// Unknown model in the breakdown: no window, no fill.
	totals.AddCall("zzz", 7, 0, 0, 0, 0, 7, 0, 0, false, 3)
	view = totals.View("s", 1000, true, func(m string) (int64, bool) {
		w, ok := windows[m]
		return w, ok
	})
	if len(view.Models) != 3 {
		t.Fatalf("got %+v", view.Models)
	}
	zz := view.Models[2]
	if zz.Model != "zzz" || zz.ContextKnown || zz.FillPercent != nil {
		t.Fatalf("zzz %+v", zz)
	}
}

func closeTo(got, want float64) bool {
	d := got - want
	if d < 0 {
		d = -d
	}
	return d < 1e-9
}

func TestUsageTotalsDaily(t *testing.T) {
	var totals UsageTotals
	// 2026-10-26 and 2026-10-27 UTC.
	totals.AddCall("m", 10, 5, 0, 0, 0, 15, 0.01, 0.02, true, 1790880000)
	totals.AddCall("m", 20, 10, 0, 0, 0, 30, 0.02, 0.04, true, 1790966400)
	if len(totals.Daily) != 2 {
		t.Fatalf("daily %+v", totals.Daily)
	}
	view := totals.View("s", 1000, true, nil)
	if len(view.Daily) != 2 || view.Daily[0].Date > view.Daily[1].Date {
		t.Fatalf("daily view %+v", view.Daily)
	}
	if view.Daily[0].TotalTokens+view.Daily[1].TotalTokens != 45 {
		t.Fatalf("daily view %+v", view.Daily)
	}
}
