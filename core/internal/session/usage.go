// Package session: usage.go owns per-session token and spend accounting.
// The agent folds every LLM call's Usage into UsageTotals, persisted as
// usage.json next to the transcript; SessionUsage is the client-facing
// view with the model's context window and fill percentage.
package session

import "sort"

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
	// Models breaks the same counters down per model id, so mid-session
	// model switches keep their own books. Top-level fields stay the
	// across-models sum for cheap reads.
	Models map[string]*ModelUsage `json:"models,omitempty"`
}

// ModelUsage is one model's slice of a session's ledger.
type ModelUsage struct {
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
	if model != "" {
		if t.Models == nil {
			t.Models = map[string]*ModelUsage{}
		}
		m := t.Models[model]
		if m == nil {
			m = &ModelUsage{}
			t.Models[model] = m
		}
		m.Calls++
		m.PromptTokens += prompt
		m.CompletionTokens += completion
		m.ReasoningTokens += reasoning
		m.CacheReadTokens += cacheRead
		m.CacheWriteTokens += cacheWrite
		m.TotalTokens += total
		if hasCost {
			m.Cost += cost
			m.ListCost += listCost
			m.HasCost = true
		}
	}
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
	// Models is the per-model breakdown, heaviest session share first.
	Models []ModelUsageView `json:"models,omitempty"`
}

// ModelUsageView is one model's client-facing slice: its own window,
// fill and spend.
type ModelUsageView struct {
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

// View builds the wire view from persisted totals. window/known cover
// the session's current model; windowOf resolves every model in the
// breakdown. FillPercent is set only for known windows.
func (t UsageTotals) View(sessionID string, window int64, known bool, windowOf func(string) (int64, bool)) SessionUsage {
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
	for name, m := range t.Models {
		if m == nil {
			continue
		}
		mv := ModelUsageView{
			Model:            name,
			Calls:            m.Calls,
			PromptTokens:     m.PromptTokens,
			CompletionTokens: m.CompletionTokens,
			ReasoningTokens:  m.ReasoningTokens,
			CacheReadTokens:  m.CacheReadTokens,
			CacheWriteTokens: m.CacheWriteTokens,
			TotalTokens:      m.TotalTokens,
			Cost:             m.Cost,
			ListCost:         m.ListCost,
			HasCost:          m.HasCost,
		}
		if windowOf != nil {
			if w, ok := windowOf(name); ok {
				mv.ContextWindow, mv.ContextKnown = w, true
				if w > 0 {
					pct := float64(m.TotalTokens) / float64(w) * 100
					mv.FillPercent = &pct
				}
			}
		}
		out.Models = append(out.Models, mv)
	}
	sort.Slice(out.Models, func(i, j int) bool {
		if out.Models[i].TotalTokens != out.Models[j].TotalTokens {
			return out.Models[i].TotalTokens > out.Models[j].TotalTokens
		}
		return out.Models[i].Model < out.Models[j].Model
	})
	return out
}
