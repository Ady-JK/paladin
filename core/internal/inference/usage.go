// Package inference: usage.go owns per-call token and spend accounting.
// Every dialect reports usage in its own shape; each one maps onto Usage
// so turns, sessions and the wire API share one vocabulary. Token counts
// come from the provider's typed response; dollar costs come from the
// OpenCode gateway extras (cost/list_cost), which only OpenCode sends.
// Plain providers leave HasCost false and costs at zero.
package inference

import "encoding/json"

// Usage is one LLM call's token and spend footprint.
type Usage struct {
	PromptTokens     int64
	CompletionTokens int64
	ReasoningTokens  int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	TotalTokens      int64
	// Cost is the gateway-reported actual charge in USD. ListCost is the
	// undiscounted list price. Both are zero when the provider did not
	// report them (HasCost false).
	Cost     float64
	ListCost float64
	HasCost  bool
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
	u.TotalTokens += o.TotalTokens
	if o.HasCost {
		u.Cost += o.Cost
		u.ListCost += o.ListCost
		u.HasCost = true
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

// gatewayCost pulls cost and cache extras out of a raw response body.
// raw is the full response JSON (completion, chunk or response); the
// usage object is found one level down. Returns ok=false when neither
// gateway sent a cost envelope, e.g. plain non-OpenCode providers.
func gatewayCost(raw string) (cost, listCost float64, cacheRead, cacheWrite int64, ok bool) {
	if raw == "" {
		return 0, 0, 0, 0, false
	}
	var outer struct {
		Usage json.RawMessage `json:"usage"`
	}
	if err := json.Unmarshal([]byte(raw), &outer); err != nil || len(outer.Usage) == 0 {
		return 0, 0, 0, 0, false
	}
	var ex opencodeExtras
	if err := json.Unmarshal(outer.Usage, &ex); err != nil {
		return 0, 0, 0, 0, false
	}
	if ex.Cost != nil {
		cost = *ex.Cost
		ok = true
	}
	if ex.Opencode != nil {
		if ex.Opencode.ListCost != nil {
			listCost = *ex.Opencode.ListCost
			ok = true
		}
		if ex.Opencode.Tokens != nil {
			cacheRead = ex.Opencode.Tokens.CacheRead
			cacheWrite = ex.Opencode.Tokens.CacheWrite5m + ex.Opencode.Tokens.CacheWrite1h
		}
	}
	if ex.PromptTokensDetails != nil && ex.PromptTokensDetails.CacheWriteTokens != nil {
		cacheWrite += *ex.PromptTokensDetails.CacheWriteTokens
	}
	return cost, listCost, cacheRead, cacheWrite, ok
}
