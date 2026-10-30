// usage.go owns per-session token and spend accounting.
// The agent folds every LLM call into UsageTotals, persisted as
// usage.json next to the transcript; SessionUsage is the client-facing
// view with the model's context window and fill percentage.

package session

import (
	"sort"
	"time"
)

// Counters is the token and spend tally every usage shape shares:
// a session, a model, a day, or the whole fleet. Costs are
// gateway-reported USD; HasCost is false when no call has ever
// reported cost (plain providers), distinguishing "free" from "unknown".
type Counters struct {
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

// Add folds o into c. Spend only moves when o reported it.
func (c *Counters) Add(o Counters) {
	c.Calls += o.Calls
	c.PromptTokens += o.PromptTokens
	c.CompletionTokens += o.CompletionTokens
	c.ReasoningTokens += o.ReasoningTokens
	c.CacheReadTokens += o.CacheReadTokens
	c.CacheWriteTokens += o.CacheWriteTokens
	c.TotalTokens += o.TotalTokens
	if o.HasCost {
		c.Cost += o.Cost
		c.ListCost += o.ListCost
		c.HasCost = true
	}
}

// UsageTotals is the persisted per-session ledger (usage.json). The
// embedded Counters are the across-models sum for cheap reads.
type UsageTotals struct {
	SessionID string `json:"sessionId"`
	Model     string `json:"model"`
	Counters
	// LastInputTokens is the previous LLM call's input (prompt) tokens.
	// The context fill reads last-call input over the model window, so
	// this — not the cumulative total — is what the gauge reports.
	LastInputTokens int64 `json:"lastInputTokens"`
	UpdatedAt       int64 `json:"updatedAt"`
	// Models breaks the same counters down per model id, so mid-session
	// model switches keep their own books.
	Models map[string]*ModelUsage `json:"models,omitempty"`
	// Daily buckets the same counters by UTC calendar day (YYYY-MM-DD),
	// so dashboards can render activity timelines and heatmaps without
	// scanning transcripts.
	Daily map[string]*Counters `json:"daily,omitempty"`
	// RecoveredFrom names the corrupt ledger this one replaced (kept
	// beside it); spend counted here is then only a lower bound.
	RecoveredFrom string `json:"recoveredFrom,omitempty"`
}

// SpendIncomplete reports that earlier spend was lost to a corrupt
// ledger, so Cost undercounts.
func (t UsageTotals) SpendIncomplete() bool { return t.RecoveredFrom != "" }

// ModelUsage is one model's slice of a session's ledger.
type ModelUsage struct {
	Counters
	// LastInputTokens mirrors UsageTotals: this model's latest call.
	LastInputTokens int64 `json:"lastInputTokens"`
}

// dayKey renders a unix epoch as a UTC calendar day.
func dayKey(now int64) string {
	return time.Unix(now, 0).UTC().Format("2006-01-02")
}

// AddCall folds one LLM call into the totals, its model's book and its
// day. call.Calls is ignored: one call is one call. The latest
// non-empty model wins as the session's current model.
func (t *UsageTotals) AddCall(model string, call Counters, now int64) {
	call.Calls = 1
	if model != "" {
		t.Model = model
	}
	t.Add(call)
	t.LastInputTokens = call.PromptTokens
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
		m.Add(call)
		m.LastInputTokens = call.PromptTokens
	}
	if now > 0 {
		if t.Daily == nil {
			t.Daily = map[string]*Counters{}
		}
		d := t.Daily[dayKey(now)]
		if d == nil {
			d = &Counters{}
			t.Daily[dayKey(now)] = d
		}
		d.Add(call)
	}
}

// SessionUsage is the client-facing usage view. FillPercent is the last
// LLM call's input tokens over the context window; nil when the model's
// window is unknown, so the UI shows raw tokens instead of a misleading
// percentage.
type SessionUsage struct {
	SessionID     string `json:"sessionId"`
	Model         string `json:"model"`
	ContextWindow int64  `json:"contextWindow"`
	ContextKnown  bool   `json:"contextKnown"`
	Counters
	LastInputTokens int64    `json:"lastInputTokens"`
	FillPercent     *float64 `json:"fillPercent,omitempty"`
	// Models is the per-model breakdown, heaviest session share first.
	Models []ModelUsageView `json:"models,omitempty"`
	// Daily is the per-day activity ledger, oldest day first.
	Daily []DailyPoint `json:"daily,omitempty"`
}

// ModelUsageView is one model's client-facing slice: its own window,
// fill and spend.
type ModelUsageView struct {
	Model         string `json:"model"`
	ContextWindow int64  `json:"contextWindow"`
	ContextKnown  bool   `json:"contextKnown"`
	Counters
	LastInputTokens int64    `json:"lastInputTokens"`
	FillPercent     *float64 `json:"fillPercent,omitempty"`
}

// DailyPoint is one calendar day's client-facing activity.
type DailyPoint struct {
	Date string `json:"date"`
	Counters
}

// fillPercent is last-call input over a known, non-zero window.
func fillPercent(lastInput, window int64, known bool) *float64 {
	if !known || window <= 0 {
		return nil
	}
	pct := float64(lastInput) / float64(window) * 100
	return &pct
}

// View builds the wire view from persisted totals. window/known cover
// the session's current model; windowOf resolves every model in the
// breakdown.
func (t UsageTotals) View(sessionID string, window int64, known bool, windowOf func(string) (int64, bool)) SessionUsage {
	out := SessionUsage{
		SessionID:       sessionID,
		Model:           t.Model,
		ContextWindow:   window,
		ContextKnown:    known,
		Counters:        t.Counters,
		LastInputTokens: t.LastInputTokens,
		FillPercent:     fillPercent(t.LastInputTokens, window, known),
	}
	for name, m := range t.Models {
		if m == nil {
			continue
		}
		mv := ModelUsageView{Model: name, Counters: m.Counters, LastInputTokens: m.LastInputTokens}
		if windowOf != nil {
			if w, ok := windowOf(name); ok {
				mv.ContextWindow, mv.ContextKnown = w, true
				mv.FillPercent = fillPercent(m.LastInputTokens, w, true)
			}
		}
		out.Models = append(out.Models, mv)
	}
	SortModelsHeaviestFirst(out.Models)
	out.Daily = DailyPoints(t.Daily)
	return out
}

// SortModelsHeaviestFirst orders by total tokens, then by name.
func SortModelsHeaviestFirst(models []ModelUsageView) {
	sort.Slice(models, func(i, j int) bool {
		if models[i].TotalTokens != models[j].TotalTokens {
			return models[i].TotalTokens > models[j].TotalTokens
		}
		return models[i].Model < models[j].Model
	})
}

// DailyPoints flattens a per-day map into an oldest-first slice.
func DailyPoints(daily map[string]*Counters) []DailyPoint {
	if len(daily) == 0 {
		return nil
	}
	dates := make([]string, 0, len(daily))
	for date, d := range daily {
		if d != nil {
			dates = append(dates, date)
		}
	}
	sort.Strings(dates)
	out := make([]DailyPoint, 0, len(dates))
	for _, date := range dates {
		out = append(out, DailyPoint{Date: date, Counters: *daily[date]})
	}
	return out
}

// OverallSessionEntry is one session's slice of the fleet-wide ledger.
type OverallSessionEntry struct {
	SessionID string `json:"sessionId"`
	Title     string `json:"title,omitempty"`
	Model     string `json:"model"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
	Counters
}

// OverallUsage is the fleet-wide rollup: every session's totals plus
// per-model, per-session and per-day breakdowns. Models and Sessions
// are heaviest-first; Daily is oldest-first.
type OverallUsage struct {
	SessionCount      int64 `json:"sessionCount"`
	SessionsWithUsage int64 `json:"sessionsWithUsage"`
	Counters
	UpdatedAt int64                 `json:"updatedAt"`
	Models    []ModelUsageView      `json:"models,omitempty"`
	Sessions  []OverallSessionEntry `json:"sessions,omitempty"`
	Daily     []DailyPoint          `json:"daily,omitempty"`
}
