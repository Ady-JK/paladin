// usage.go owns per-call token and spend accounting.
// Every dialect reports usage in its own shape; each one maps onto Usage
// so turns, sessions and the wire API share one vocabulary. Token counts
// come from the provider's typed response; dollar costs come from the
// gateway: usage.cost in the body (OpenCode, OpenRouter, and LiteLLM when
// streaming), OpenCode's list_cost, or LiteLLM's cost header on
// non-streamed calls. Plain providers leave HasCost false and costs at zero.

package inference

import (
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"strings"
)

// Usage is one LLM call's token and spend footprint.
type Usage struct {
	PromptTokens     int64
	CompletionTokens int64
	ReasoningTokens  int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	CacheReadKnown   bool
	CacheWriteKnown  bool
	TotalTokens      int64
	// Cost is the gateway-reported actual charge in USD. ListCost is the
	// undiscounted list price. Both are zero when the provider did not
	// report them (HasCost false).
	Cost     float64
	ListCost float64
	HasCost  bool
	// CostEstimated marks a cost the gateway did not charge: a
	// subscription plan (OpenCode Go), a free tier, or a gateway that
	// reports list_cost but no cost. Cost then holds the plan's list
	// price and EstimatedCost() prefers it. Without this flag a
	// subscription would report $0 for a million tokens and read as
	// free, which is what the analytics pages used to show.
	CostEstimated bool
}

// EstimatedCost is the dollar figure to show for this call: the actual
// charge when the gateway made one, else the list price of a
// subscription or free call.
func (u Usage) EstimatedCost() float64 {
	if u.CostEstimated {
		return u.ListCost
	}
	return u.Cost
}

// Empty reports whether nothing was recorded.
func (u Usage) Empty() bool {
	return u == Usage{}
}

// Add folds another call's usage into this total. Costs add only when the
// other side reported them; a free-model call (cost 0, reported) still
// counts as reported.
func (u *Usage) Add(o Usage) {
	u.PromptTokens += o.PromptTokens
	u.CompletionTokens += o.CompletionTokens
	u.ReasoningTokens += o.ReasoningTokens
	u.CacheReadTokens += o.CacheReadTokens
	u.CacheWriteTokens += o.CacheWriteTokens
	u.CacheReadKnown = u.CacheReadKnown || o.CacheReadKnown
	u.CacheWriteKnown = u.CacheWriteKnown || o.CacheWriteKnown
	u.TotalTokens += o.TotalTokens
	if o.HasCost {
		u.Cost += o.Cost
		u.ListCost += o.ListCost
		u.HasCost = true
		u.CostEstimated = u.CostEstimated || o.CostEstimated
	}
}

// addUsage folds u into acc, tolerating a nil report (providers that
// stayed silent contribute nothing).
func addUsage(acc *Usage, u *Usage) {
	if u != nil {
		acc.Add(*u)
	}
}

// opencodeExtras is the gateway cost envelope nested under usage.
// OpenCode sends {"cost":…,"opencode":{"list_cost":…,"tokens":{…}}};
// OpenRouter sends {"cost":…} with cache detail in
// prompt_tokens_details. Pointers distinguish "reported zero" (a free
// model call) from "not reported" (plain providers).
type opencodeExtras struct {
	Cost     *float64 `json:"cost"`
	Opencode *struct {
		ListCost *float64 `json:"list_cost"`
		Tokens   *struct {
			Input        int64 `json:"input"`
			Output       int64 `json:"output"`
			Reasoning    int64 `json:"reasoning"`
			CacheRead    int64 `json:"cache_read"`
			CacheWrite5m int64 `json:"cache_write_5m"`
			CacheWrite1h int64 `json:"cache_write_1h"`
		} `json:"tokens"`
	} `json:"opencode"`
	PromptTokensDetails *struct {
		CacheWriteTokens *int64 `json:"cache_write_tokens"`
	} `json:"prompt_tokens_details"`
}

// gatewayEnvelope is a cost envelope as parsed from a response body.
// HasCost and HasList record which numbers the gateway actually sent,
// which is what distinguishes "charged nothing" from "said nothing".
type gatewayEnvelope struct {
	Cost, ListCost        float64
	CacheRead, CacheWrite int64
	HasCost, HasList      bool
}

// gatewayCost pulls cost and cache extras out of a raw response body.
// raw is the full response JSON (completion, chunk or response); the
// usage object is found one level down. ok is false when the body sent
// no cost envelope at all, e.g. a plain non-gateway provider.
func gatewayCost(raw string) (gatewayEnvelope, bool) {
	var ge gatewayEnvelope
	if raw == "" {
		return ge, false
	}
	var outer struct {
		Usage json.RawMessage `json:"usage"`
	}
	if err := json.Unmarshal([]byte(raw), &outer); err != nil || len(outer.Usage) == 0 {
		return ge, false
	}
	var ex opencodeExtras
	if err := json.Unmarshal(outer.Usage, &ex); err != nil {
		return ge, false
	}
	if ex.Cost != nil {
		ge.Cost, ge.HasCost = *ex.Cost, true
	}
	if ex.Opencode != nil {
		if ex.Opencode.ListCost != nil {
			ge.ListCost, ge.HasList = *ex.Opencode.ListCost, true
		}
		if ex.Opencode.Tokens != nil {
			ge.CacheRead = ex.Opencode.Tokens.CacheRead
			ge.CacheWrite = ex.Opencode.Tokens.CacheWrite5m + ex.Opencode.Tokens.CacheWrite1h
		}
	}
	if ex.PromptTokensDetails != nil && ex.PromptTokensDetails.CacheWriteTokens != nil {
		ge.CacheWrite += *ex.PromptTokensDetails.CacheWriteTokens
	}
	return ge, ge.HasCost || ge.HasList
}

// applyGatewayCost records a gateway cost envelope on u.
//
// A gateway that reports only list_cost (a subscription plan such as
// OpenCode Go, or a free tier) is charged nothing for the tokens it
// served, so Cost stays 0 — but the list price is what those tokens
// would have cost, and reporting a flat $0 for millions of tokens reads
// as broken. CostEstimated marks that case so the UI shows the list
// price instead. A gateway that reports neither number gave no cost
// data at all; it stays unknown, so a plain provider is never mistaken
// for a free one.
func applyGatewayCost(u *Usage, cost, listCost float64, hasCost, hasList bool) {
	if !hasCost && !hasList {
		return
	}
	u.HasCost = true
	u.Cost, u.ListCost = cost, listCost
	// Charged nothing for tokens that carry a list price: a subscription
	// plan (OpenCode Go sends an explicit cost 0 beside a nonzero
	// list_cost) or a free tier. The plan did not bill, but $0 for
	// millions of tokens reads as broken, so the list price is shown and
	// marked as an estimate. A zero with no list price is a gateway
	// saying "nothing to charge" — a real free call, no estimate.
	if cost <= 0 && hasList && listCost > 0 {
		u.CostEstimated = true
	}
}

// liteLLMCostHeader carries the call's USD cost on LiteLLM responses.
// Streamed calls also report it as usage.cost in the final chunk, but a
// non-streamed body leaves it out, so the header is the only source there.
const liteLLMCostHeader = "x-litellm-response-cost"

// withHeaderCost fills in a cost the body did not report from the
// response's LiteLLM cost header. Garbage or negative values are
// ignored rather than trusted.
func withHeaderCost(u *Usage, res *http.Response) *Usage {
	if res == nil || (u != nil && u.HasCost) {
		return u
	}
	raw := strings.TrimSpace(res.Header.Get(liteLLMCostHeader))
	if raw == "" {
		return u
	}
	cost, err := strconv.ParseFloat(raw, 64)
	if err != nil || cost < 0 || math.IsNaN(cost) || math.IsInf(cost, 0) {
		return u
	}
	if u == nil {
		u = &Usage{}
	}
	u.Cost, u.HasCost = cost, true
	return u
}

// captureCacheDetails records whether cache counts were actually reported.
func captureCacheDetails(out *Usage, raw, details string) {
	var envelope struct {
		Usage map[string]json.RawMessage `json:"usage"`
	}
	if json.Unmarshal([]byte(raw), &envelope) != nil {
		return
	}
	var values struct {
		Cached *int64 `json:"cached_tokens"`
		Write  *int64 `json:"cache_write_tokens"`
	}
	if json.Unmarshal(envelope.Usage[details], &values) != nil {
		return
	}
	if values.Cached != nil {
		out.CacheReadKnown = true
		out.CacheReadTokens = *values.Cached
	}
	if values.Write != nil {
		out.CacheWriteKnown = true
		out.CacheWriteTokens = *values.Write
	}
}
