package inference

import (
	"testing"
)

// TestGatewayCostSubscriptionIsEstimated covers the case the analytics
// pages showed as $0: a subscription gateway (OpenCode Go) sends
// list_cost but no cost. The tokens were not free, so the usage must
// say "estimated from the list price", not "zero".
func TestGatewayCostSubscriptionIsEstimated(t *testing.T) {
	raw := `{"usage":{"list_cost":0.16143655,` +
		`"opencode":{"list_cost":0.16143655,"tokens":{"input":18042705,"output":69216,"cache_read":17876096}}}}`
	ge, ok := gatewayCost(raw)
	if !ok {
		t.Fatal("gateway envelope not detected")
	}
	if ge.HasCost {
		t.Error("no cost was sent, so HasCost must be false")
	}
	if !ge.HasList {
		t.Fatal("list_cost was sent but HasList is false")
	}

	u := &Usage{PromptTokens: 18042705, CompletionTokens: 69216, TotalTokens: 18111921}
	applyGatewayCost(u, ge.Cost, ge.ListCost, ge.HasCost, ge.HasList)
	if !u.HasCost {
		t.Fatal("a list price is still a reported cost")
	}
	if !u.CostEstimated {
		t.Fatal("a subscription call must be marked estimated")
	}
	if u.Cost != 0 {
		t.Fatalf("charged cost = %v, want 0", u.Cost)
	}
	if got, want := u.EstimatedCost(), 0.16143655; got != want {
		t.Fatalf("EstimatedCost = %v, want %v", got, want)
	}
}

// TestGatewayCostChargedIsNotEstimated: a gateway that reports a real
// charge must keep it and must not be labelled an estimate.
func TestGatewayCostChargedIsNotEstimated(t *testing.T) {
	raw := `{"usage":{"cost":0.02,"opencode":{"list_cost":0.05}}}`
	ge, ok := gatewayCost(raw)
	if !ok || !ge.HasCost {
		t.Fatalf("expected a charged envelope, got %+v ok=%v", ge, ok)
	}
	u := &Usage{TotalTokens: 100}
	applyGatewayCost(u, ge.Cost, ge.ListCost, ge.HasCost, ge.HasList)
	if u.CostEstimated {
		t.Fatal("a billed call must not be marked estimated")
	}
	if got := u.EstimatedCost(); got != 0.02 {
		t.Fatalf("EstimatedCost = %v, want the 0.02 actually charged", got)
	}
}

// TestGatewayCostZeroIsFreeNotEstimated: an explicit cost of 0 with no
// list price is a genuinely free call, not missing data.
func TestGatewayCostZeroIsFreeNotEstimated(t *testing.T) {
	ge, ok := gatewayCost(`{"usage":{"cost":0}}`)
	if !ok || !ge.HasCost {
		t.Fatalf("expected a reported cost, got %+v ok=%v", ge, ok)
	}
	u := &Usage{TotalTokens: 100}
	applyGatewayCost(u, ge.Cost, ge.ListCost, ge.HasCost, ge.HasList)
	if !u.HasCost {
		t.Fatal("a reported 0 is still reported")
	}
	if u.CostEstimated {
		t.Fatal("an explicit 0 with no list price is free, not an estimate")
	}
	if got := u.EstimatedCost(); got != 0 {
		t.Fatalf("EstimatedCost = %v, want 0", got)
	}
}

// TestGatewayCostAbsentStaysUnknown: a plain provider that reports no
// cost envelope must stay unknown, so it is never shown as free.
func TestGatewayCostAbsentStaysUnknown(t *testing.T) {
	for _, raw := range []string{
		`{"usage":{"prompt_tokens":10,"completion_tokens":2}}`,
		``,
		`not json`,
	} {
		if _, ok := gatewayCost(raw); ok {
			t.Fatalf("raw %q was treated as a cost envelope", raw)
		}
	}
	u := &Usage{TotalTokens: 12}
	applyGatewayCost(u, 0, 0, false, false)
	if u.HasCost {
		t.Fatal("no envelope must leave the cost unknown")
	}
}

// TestUsageAddKeepsEstimated: folding calls keeps the estimated marker,
// so a session mixing billed and subscription calls still reads right.
func TestUsageAddKeepsEstimated(t *testing.T) {
	var acc Usage
	acc.Add(Usage{Cost: 0.01, HasCost: true})
	acc.Add(Usage{ListCost: 0.05, HasCost: true, CostEstimated: true})
	if !acc.CostEstimated {
		t.Fatal("the estimate marker was lost when folding calls")
	}
	if got, want := acc.EstimatedCost(), 0.05; got != want {
		t.Fatalf("EstimatedCost = %v, want the list price %v", got, want)
	}
}

// TestChatUsageSubscriptionEnvelope is the wire-level regression: the
// exact body shape the session's usage.json came from. cost 0 with a
// nested opencode.list_cost is what OpenCode Go sends for a
// subscription call.
func TestChatUsageSubscriptionEnvelope(t *testing.T) {
	const raw = `{"id":"x","usage":{"prompt_tokens":18042705,"completion_tokens":69216,` +
		`"total_tokens":18111921,"cost":0,` +
		`"opencode":{"list_cost":0.16143655,"tokens":{"input":18042705,"output":69216}}}}`
	ge, ok := gatewayCost(raw)
	if !ok {
		t.Fatal("envelope not found")
	}
	if !ge.HasCost || !ge.HasList {
		t.Fatalf("subscription body must report both numbers: %+v", ge)
	}
	u := &Usage{TotalTokens: 18111921}
	applyGatewayCost(u, ge.Cost, ge.ListCost, ge.HasCost, ge.HasList)
	if !u.CostEstimated {
		t.Fatal("cost 0 with a list price must be marked estimated")
	}
	if got, want := u.EstimatedCost(), 0.16143655; got != want {
		t.Fatalf("EstimatedCost = %v, want %v", got, want)
	}
}
