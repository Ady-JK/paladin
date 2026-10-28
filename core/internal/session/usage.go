// Package session: usage.go owns per-session token and spend accounting.
// The agent folds every LLM call's Usage into UsageTotals, persisted as
// usage.json next to the transcript; SessionUsage is the client-facing
// view with the model's context window and fill percentage.
package session

import (
	"sort"
	"time"
)

// UsageTotals is the persisted per-session accumulator. Costs are
// gateway-reported USD; HasCost is false when no call has ever reported
// cost (plain providers), distinguishing "free" from "unknown".
type UsageTotals struct {
	SessionID        string `json:"session_id"`
	Model            string `json:"model"`
	Calls            int64  `json:"calls"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	ReasoningTokens  int64  `json:"reasoning_tokens"`
	CacheReadTokens  int64  `json:"cache_read_tokens"`
	CacheWriteTokens int64  `json:"cache_write_tokens"`
	TotalTokens      int64  `json:"total_tokens"`
	// LastInputTokens is the previous LLM call's input (prompt) tokens.
	// The context fill reads last-call input over the model window, so
	// this — not the cumulative total — is what the gauge reports.
	LastInputTokens int64   `json:"last_input_tokens"`
	Cost            float64 `json:"cost"`
	ListCost        float64 `json:"list_cost"`
	HasCost         bool    `json:"has_cost"`
	UpdatedAt       int64   `json:"updated_at"`
	// Models breaks the same counters down per model id, so mid-session
	// model switches keep their own books. Top-level fields stay the
	// across-models sum for cheap reads.
	Models map[string]*ModelUsage `json:"models,omitempty"`
	// Daily buckets the same counters by UTC calendar day (YYYY-MM-DD),
	// so dashboards can render activity timelines and heatmaps without
	// scanning transcripts. Backfilled ledgers simply have no entries
	// until new turns bill.
	Daily map[string]*DayUsage `json:"daily,omitempty"`
}

// DayUsage is one calendar day's slice of a session's ledger.
type DayUsage struct {
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

// dayKey renders a unix epoch as a UTC calendar day.
func dayKey(now int64) string {
	return time.Unix(now, 0).UTC().Format("2006-01-02")
}

// ModelUsage is one model's slice of a session's ledger.
type ModelUsage struct {
	Calls            int64 `json:"calls"`
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	ReasoningTokens  int64 `json:"reasoning_tokens"`
	CacheReadTokens  int64 `json:"cache_read_tokens"`
	CacheWriteTokens int64 `json:"cache_write_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
	// LastInputTokens mirrors UsageTotals: this model's latest call.
	LastInputTokens int64   `json:"last_input_tokens"`
	Cost            float64 `json:"cost"`
	ListCost        float64 `json:"list_cost"`
	HasCost         bool    `json:"has_cost"`
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
	t.LastInputTokens = prompt
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
		m.LastInputTokens = prompt
		if hasCost {
			m.Cost += cost
			m.ListCost += listCost
			m.HasCost = true
		}
	}
	if now > 0 {
		if t.Daily == nil {
			t.Daily = map[string]*DayUsage{}
		}
		d := t.Daily[dayKey(now)]
		if d == nil {
			d = &DayUsage{}
			t.Daily[dayKey(now)] = d
		}
		d.Calls++
		d.PromptTokens += prompt
		d.CompletionTokens += completion
		d.ReasoningTokens += reasoning
		d.CacheReadTokens += cacheRead
		d.CacheWriteTokens += cacheWrite
		d.TotalTokens += total
		if hasCost {
			d.Cost += cost
			d.ListCost += listCost
			d.HasCost = true
		}
	}
}

// SessionUsage is the client-facing usage view (camelCase, like
// SessionInfo). FillPercent is the last LLM call's input tokens over
// the context window; nil when the model's window is unknown, so the
// UI shows raw tokens instead of a misleading percentage.
type SessionUsage struct {
	SessionID        string `json:"sessionId"`
	Model            string `json:"model"`
	ContextWindow    int64  `json:"contextWindow"`
	ContextKnown     bool   `json:"contextKnown"`
	Calls            int64  `json:"calls"`
	PromptTokens     int64  `json:"promptTokens"`
	CompletionTokens int64  `json:"completionTokens"`
	ReasoningTokens  int64  `json:"reasoningTokens"`
	CacheReadTokens  int64  `json:"cacheReadTokens"`
	CacheWriteTokens int64  `json:"cacheWriteTokens"`
	TotalTokens      int64  `json:"totalTokens"`
	// LastInputTokens is the previous LLM call's input tokens; the fill
	// percentage reads this over the context window.
	LastInputTokens int64    `json:"lastInputTokens"`
	Cost            float64  `json:"cost"`
	ListCost        float64  `json:"listCost"`
	HasCost         bool     `json:"hasCost"`
	FillPercent     *float64 `json:"fillPercent,omitempty"`
	// Models is the per-model breakdown, heaviest session share first.
	Models []ModelUsageView `json:"models,omitempty"`
	// Daily is the per-day activity ledger, oldest day first, for the
	// session timeline and heatmap. Empty for ledgers that predate
	// daily bucketing.
	Daily []DailyPoint `json:"daily,omitempty"`
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
	LastInputTokens  int64    `json:"lastInputTokens"`
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
		LastInputTokens:  t.LastInputTokens,
		Cost:             t.Cost,
		ListCost:         t.ListCost,
		HasCost:          t.HasCost,
	}
	if known && window > 0 {
		pct := float64(t.LastInputTokens) / float64(window) * 100
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
			LastInputTokens:  m.LastInputTokens,
			Cost:             m.Cost,
			ListCost:         m.ListCost,
			HasCost:          m.HasCost,
		}
		if windowOf != nil {
			if w, ok := windowOf(name); ok {
				mv.ContextWindow, mv.ContextKnown = w, true
				if w > 0 {
					pct := float64(m.LastInputTokens) / float64(w) * 100
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
	out.Daily = dailyPoints(t.Daily)
	return out
}

// DailyPoint is one calendar day's client-facing activity.
type DailyPoint struct {
	Date             string  `json:"date"`
	Calls            int64   `json:"calls"`
	PromptTokens     int64   `json:"promptTokens"`
	CompletionTokens int64   `json:"completionTokens"`
	ReasoningTokens  int64   `json:"reasoningTokens"`
	CacheReadTokens  int64   `json:"cacheReadTokens"`
	CacheWriteTokens int64   `json:"cacheWriteTokens"`
	TotalTokens      int64   `json:"totalTokens"`
	Cost             float64 `json:"cost"`
	ListCost         float64 `json:"listCost"`
	HasCost          bool    `json:"hasCost"`
}

// dailyPoints flattens the per-day map into an oldest-first slice.
func dailyPoints(daily map[string]*DayUsage) []DailyPoint {
	if len(daily) == 0 {
		return nil
	}
	dates := make([]string, 0, len(daily))
	for date, d := range daily {
		if d == nil {
			continue
		}
		dates = append(dates, date)
	}
	sort.Strings(dates)
	out := make([]DailyPoint, 0, len(dates))
	for _, date := range dates {
		d := daily[date]
		out = append(out, DailyPoint{
			Date:             date,
			Calls:            d.Calls,
			PromptTokens:     d.PromptTokens,
			CompletionTokens: d.CompletionTokens,
			ReasoningTokens:  d.ReasoningTokens,
			CacheReadTokens:  d.CacheReadTokens,
			CacheWriteTokens: d.CacheWriteTokens,
			TotalTokens:      d.TotalTokens,
			Cost:             d.Cost,
			ListCost:         d.ListCost,
			HasCost:          d.HasCost,
		})
	}
	return out
}

// OverallSessionEntry is one session's slice of the fleet-wide ledger.
type OverallSessionEntry struct {
	SessionID        string  `json:"sessionId"`
	Title            string  `json:"title,omitempty"`
	Model            string  `json:"model"`
	CreatedAt        int64   `json:"createdAt"`
	UpdatedAt        int64   `json:"updatedAt"`
	Calls            int64   `json:"calls"`
	PromptTokens     int64   `json:"promptTokens"`
	CompletionTokens int64   `json:"completionTokens"`
	ReasoningTokens  int64   `json:"reasoningTokens"`
	CacheReadTokens  int64   `json:"cacheReadTokens"`
	CacheWriteTokens int64   `json:"cacheWriteTokens"`
	TotalTokens      int64   `json:"totalTokens"`
	Cost             float64 `json:"cost"`
	ListCost         float64 `json:"listCost"`
	HasCost          bool    `json:"hasCost"`
}

// OverallUsage is the fleet-wide rollup: every session's totals plus
// per-model, per-session and per-day breakdowns. Models and Sessions
// are heaviest-first; Daily is oldest-first.
type OverallUsage struct {
	SessionCount      int64                 `json:"sessionCount"`
	SessionsWithUsage int64                 `json:"sessionsWithUsage"`
	Calls             int64                 `json:"calls"`
	PromptTokens      int64                 `json:"promptTokens"`
	CompletionTokens  int64                 `json:"completionTokens"`
	ReasoningTokens   int64                 `json:"reasoningTokens"`
	CacheReadTokens   int64                 `json:"cacheReadTokens"`
	CacheWriteTokens  int64                 `json:"cacheWriteTokens"`
	TotalTokens       int64                 `json:"totalTokens"`
	Cost              float64               `json:"cost"`
	ListCost          float64               `json:"listCost"`
	HasCost           bool                  `json:"hasCost"`
	UpdatedAt         int64                 `json:"updatedAt"`
	Models            []ModelUsageView      `json:"models,omitempty"`
	Sessions          []OverallSessionEntry `json:"sessions,omitempty"`
	Daily             []DailyPoint          `json:"daily,omitempty"`
}
