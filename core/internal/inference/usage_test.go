package inference

import "testing"

func TestUsageAddAndEmpty(t *testing.T) {
	var u Usage
	if !u.Empty() {
		t.Fatal("zero usage should be empty")
	}
	u.Add(Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15})
	u.Add(Usage{PromptTokens: 3, Cost: 0.01, ListCost: 0.02, HasCost: true})
	if u.PromptTokens != 13 || u.CompletionTokens != 5 || u.TotalTokens != 15 {
		t.Fatalf("got %+v", u)
	}
	if !u.HasCost || u.Cost != 0.01 || u.ListCost != 0.02 {
		t.Fatalf("got %+v", u)
	}
	// A silent call adds nothing, not even cost state.
	before := u
	u.Add(Usage{})
	if u != before {
		t.Fatalf("got %+v want %+v", u, before)
	}
}

func TestGatewayCostOpencodeShape(t *testing.T) {
	raw := `{"id":"chatcmpl-1","usage":{"prompt_tokens":53,"completion_tokens":8,"total_tokens":61,"cost":0,"opencode":{"billing_source":"go","list_cost":0.000772,"tokens":{"input":53,"output":8,"reasoning":7,"cache_read":10,"cache_write_5m":2,"cache_write_1h":3}}}}`
	cost, list, cr, cw, ok := gatewayCost(raw)
	if !ok || cost != 0 || list != 0.000772 || cr != 10 || cw != 5 {
		t.Fatalf("got %v %v %v %v %v", cost, list, cr, cw, ok)
	}
}

func TestGatewayCostOpenRouterShape(t *testing.T) {
	raw := `{"id":"gen-1","usage":{"prompt_tokens":10,"completion_tokens":4,"total_tokens":14,"cost":0.00014,"prompt_tokens_details":{"cache_write_tokens":6}}}`
	cost, list, _, cw, ok := gatewayCost(raw)
	if !ok || cost != 0.00014 || list != 0 || cw != 6 {
		t.Fatalf("got %v %v %v %v", cost, list, cw, ok)
	}
}

func TestGatewayCostAbsent(t *testing.T) {
	for _, raw := range []string{
		"",
		`{}`,
		`{"usage":{"prompt_tokens":1,"total_tokens":1}}`,
		`{"usage":null}`,
		`not json`,
	} {
		if _, _, _, _, ok := gatewayCost(raw); ok {
			t.Fatalf("ok for %q", raw)
		}
	}
}
