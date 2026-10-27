// Package session: usage.go owns per-session token and spend accounting.
// The agent folds every LLM call's Usage into UsageTotals, persisted as
// usage.json next to the transcript; SessionUsage is the client-facing
// view with the model's context window and fill percentage.
package session

// UsageTotals is the persisted per-session accumulator. Costs are
// gateway-reported USD; HasCost is false when no call has ever reported
// cost (plain providers), distinguishing "free" from "unknown".
type UsageTotals struct {
	SessionID        string  `json:"session_id"`
	Model            string  `json:"model"`
	Calls            int64   `json:"calls"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	ReasoningTokens  int64   `json:"reasoning_tokens"`
	CacheReadTokens  int64   `json:"cache_read_tokens"`
	CacheWriteTokens int64   `json:"cache_write_tokens"`
	TotalTokens      int64   `json:"total_tokens"`
	Cost             float64 `json:"cost"`
	ListCost         float64 `json:"list_cost"`
	HasCost          bool    `json:"has_cost"`
	UpdatedAt        int64   `json:"updated_at"`
}

// AddCall folds one LLM call into the totals. model is the call's model;
// the latest one wins as the session's current model.
func (t *UsageTotals) AddCall(model string, prompt, completion, reasoning, cacheRead, cacheWrite, total int64, cost, listCost float64, hasCost bool, now int64) {
	if model != "" {
		t.Model = model
	}
	t.Calls++
	t.PromptTokens += prompt
	t.CompletionTokens += completion
	t.ReasoningTokens += reasoning
	t.CacheReadTokens += cacheRead
	t.CacheWriteTokens += cacheWrite
	t.TotalTokens += total
	if hasCost {
		t.Cost += cost
		t.ListCost += listCost
		t.HasCost = true
	}
	t.UpdatedAt = now
}

// SessionUsage is the client-facing usage view (camelCase, like
// SessionInfo). FillPercent is nil when the model's window is unknown,
// so the UI shows raw tokens instead of a misleading percentage.
type SessionUsage struct {
	SessionID        string   `json:"sessionId"`
	Model            string   `json:"model"`
	ContextWindow    int64    `json:"contextWindow"`
	ContextKnown     bool     `json:"contextKnown"`
	Calls            int64    `json:"calls"`
	PromptTokens     int64    `json:"promptTokens"`
	CompletionTokens int64    `json:"completionTokens"`
	ReasoningTokens  int64    `json:"reasoningTokens"`
	CacheReadTokens  int64    `json:"cacheReadTokens"`
	CacheWriteTokens int64    `json:"cacheWriteTokens"`
	TotalTokens      int64    `json:"totalTokens"`
	Cost             float64  `json:"cost"`
	ListCost         float64  `json:"listCost"`
	HasCost          bool     `json:"hasCost"`
	FillPercent      *float64 `json:"fillPercent,omitempty"`
}

// View builds the wire view from persisted totals. window is the model's
// total context window, known=false when the model has no table entry.
func (t UsageTotals) View(sessionID string, window int64, known bool) SessionUsage {
	out := SessionUsage{
		SessionID:        sessionID,
		Model:            t.Model,
		ContextWindow:    window,
		ContextKnown:     known,
		Calls:            t.Calls,
		PromptTokens:     t.PromptTokens,
		CompletionTokens: t.CompletionTokens,
		ReasoningTokens:  t.ReasoningTokens,
		CacheReadTokens:  t.CacheReadTokens,
		CacheWriteTokens: t.CacheWriteTokens,
		TotalTokens:      t.TotalTokens,
		Cost:             t.Cost,
		ListCost:         t.ListCost,
		HasCost:          t.HasCost,
	}
	if known && window > 0 {
		pct := float64(t.TotalTokens) / float64(window) * 100
		out.FillPercent = &pct
	}
	return out
}
