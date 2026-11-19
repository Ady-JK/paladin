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
	Calls            int64    `json:"calls"`
	UnpricedCalls    int64    `json:"unpricedCalls,omitempty"`
	TrackedSpend     *float64 `json:"trackedSpend,omitempty"`
	PromptTokens     int64    `json:"promptTokens"`
	CompletionTokens int64    `json:"completionTokens"`
	ReasoningTokens  int64    `json:"reasoningTokens"`
	CacheReadTokens  int64    `json:"cacheReadTokens"`
	CacheWriteTokens int64    `json:"cacheWriteTokens"`
	// CacheHitSum adds up each call's own cache share (cache-read tokens over
	// that call's whole prompt, 0..1) across the CacheHitCalls calls that
	// reported their cache. Their average is the cache hit rate: every call
	// weighs the same, however large its prompt.
	CacheHitSum   float64 `json:"cacheHitSum,omitempty"`
	CacheHitCalls int64   `json:"cacheHitCalls,omitempty"`
	TotalTokens   int64   `json:"totalTokens"`
	Cost          float64 `json:"cost"`
	ListCost      float64 `json:"listCost"`
	HasCost       bool    `json:"hasCost"`
	// CostEstimated marks spend the gateway did not charge: a
	// subscription plan or free tier, where Cost is 0 and ListCost is
	// what the tokens would have cost. The UI shows ListCost then, so a
	// subscription reads as "est. value" instead of a misleading $0.
	CostEstimated bool `json:"costEstimated,omitempty"`
}

// Add folds o into c. Spend only moves when o reported it.
func (c *Counters) Add(o Counters) {
	spend := c.SpendUsd()
	if o.HasCost {
		spend += o.SpendUsd()
	}
	if c.HasCost || o.HasCost {
		c.TrackedSpend = &spend
	}
	c.Calls += o.Calls
	c.UnpricedCalls += o.UnpricedCalls
	c.PromptTokens += o.PromptTokens
	c.CompletionTokens += o.CompletionTokens
	c.ReasoningTokens += o.ReasoningTokens
	c.CacheReadTokens += o.CacheReadTokens
	c.CacheWriteTokens += o.CacheWriteTokens
	c.CacheHitSum += o.CacheHitSum
	c.CacheHitCalls += o.CacheHitCalls
	c.TotalTokens += o.TotalTokens
	if o.HasCost {
		c.Cost += o.Cost
		c.ListCost += o.ListCost
		c.HasCost = true
		// A call that was priced but not charged is a subscription or
		// free plan. Older ledgers have no marker, so infer it from the
		// numbers themselves: charge 0 beside a nonzero list price.
		if o.CostEstimated || (o.Cost <= 0 && o.ListCost > 0) {
			c.CostEstimated = true
		}
	}
	c.CostEstimated = c.CostEstimated || o.CostEstimated
}

// CacheShare is one call's cache share: the part of its prompt served from
// the cache, 0..1.
func CacheShare(prompt, cacheRead int64) float64 {
	if prompt <= 0 || cacheRead <= 0 {
		return 0
	}
	return min(float64(cacheRead)/float64(prompt), 1)
}

// Estimated reports whether SpendUsd is the list price of tokens the
// plan did not bill. Ledgers written before CostEstimated existed are
// inferred from "charged 0 but priced above 0", so an upgrade does not
// turn a subscription session back into a misleading $0.
func (c Counters) Estimated() bool {
	return c.CostEstimated || (c.Cost <= 0 && c.ListCost > 0)
}

// SpendUsd is the dollar figure to show: the amount charged, or, for a
// subscription or free plan that charged nothing, the list price of
// what it served.
func (c Counters) SpendUsd() float64 {
	if c.TrackedSpend != nil {
		return *c.TrackedSpend
	}
	if c.Estimated() && c.ListCost > 0 {
		return c.ListCost
	}
	return c.Cost
}

// UsageTotals is the persisted per-session ledger (usage.json). The
// embedded Counters are the across-models sum for cheap reads.
type UsageTotals struct {
	// Schema 2 counts a call's cached input inside PromptTokens, whatever the
	// provider's own convention. Older ledgers are brought to it when read.
	Schema    int    `json:"schema,omitempty"`
	SessionID string `json:"sessionId"`
	Model     string `json:"model"`
	Counters
	// LastInputTokens is the previous LLM call's input (prompt) tokens.
	// The context fill reads last-call input over the model window, so
	// this — not the cumulative total — is what the gauge reports.
	LastInputTokens int64 `json:"lastInputTokens"`
	// ContextTokens and ContextWindow are what a harness (Codex) reported
	// about its own context after its latest call: how many tokens the
	// conversation holds and the window it really runs with. They replace
	// the input-over-table guess, which a harness's own prompt and model
	// make wrong.
	ContextTokens int64 `json:"contextTokens,omitempty"`
	ContextWindow int64 `json:"contextWindow,omitempty"`
	UpdatedAt     int64 `json:"updatedAt"`
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

// SpendIncomplete reports a corrupt ledger or calls with unknown costs.
func (t UsageTotals) SpendIncomplete() bool { return t.RecoveredFrom != "" || t.UnpricedCalls > 0 }

// ModelUsage is one model's slice of a session's ledger.
type ModelUsage struct {
	Counters
	// LastInputTokens mirrors UsageTotals: this model's latest call.
	LastInputTokens int64 `json:"lastInputTokens"`
	ContextTokens   int64 `json:"contextTokens,omitempty"`
	ContextWindow   int64 `json:"contextWindow,omitempty"`
}

// ContextReading is a harness's own report of its context: the tokens the
// conversation holds after a call and the window of the model running it.
// Window is 0 when the harness did not say.
type ContextReading struct {
	Tokens int64
	Window int64
}

// SetContext records a harness's report for the session and for model. A
// report without a window keeps the last one it gave.
func (t *UsageTotals) SetContext(model string, r ContextReading) {
	t.ContextTokens = r.Tokens
	if r.Window > 0 {
		t.ContextWindow = r.Window
	}
	if model == "" {
		return
	}
	if t.Models == nil {
		t.Models = map[string]*ModelUsage{}
	}
	m := t.Models[model]
	if m == nil {
		m = &ModelUsage{}
		t.Models[model] = m
	}
	m.ContextTokens = r.Tokens
	if r.Window > 0 {
		m.ContextWindow = r.Window
	}
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
	if call.CacheHitCalls == 0 && call.CacheHitSum == 0 && call.CacheReadTokens > 0 && call.PromptTokens > 0 {
		call.CacheHitCalls, call.CacheHitSum = 1, CacheShare(call.PromptTokens, call.CacheReadTokens)
	}
	if !call.HasCost {
		call.UnpricedCalls = 1
	}
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
	LastInputTokens int64 `json:"lastInputTokens"`
	// ContextTokens is what the fill is measured on when the provider
	// reported its own context (a harness); otherwise it is absent and the
	// fill is the last call's input.
	ContextTokens int64    `json:"contextTokens,omitempty"`
	FillPercent   *float64 `json:"fillPercent,omitempty"`
	// Limits are the plan's usage windows (a Codex account's five hours and
	// week), when the session's harness reports them.
	Limits []LimitWindow `json:"limits,omitempty"`
	// Models is the per-model breakdown, heaviest session share first.
	Models []ModelUsageView `json:"models,omitempty"`
	// Daily is the per-day activity ledger, oldest day first.
	Daily []DailyPoint `json:"daily,omitempty"`
}

// LimitWindow is one usage window of a plan: how much of it is used and when
// it renews (unix seconds, 0 when unknown).
type LimitWindow struct {
	Label       string  `json:"label"`
	UsedPercent float64 `json:"usedPercent"`
	ResetsAt    int64   `json:"resetsAt,omitempty"`
}

// ModelUsageView is one model's client-facing slice: its own window,
// fill and spend.
type ModelUsageView struct {
	Model         string `json:"model"`
	ContextWindow int64  `json:"contextWindow"`
	ContextKnown  bool   `json:"contextKnown"`
	Counters
	LastInputTokens int64    `json:"lastInputTokens"`
	ContextTokens   int64    `json:"contextTokens,omitempty"`
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
	used, contextTokens := t.LastInputTokens, int64(0)
	if t.ContextWindow > 0 {
		window, known, used, contextTokens = t.ContextWindow, true, t.ContextTokens, t.ContextTokens
	}
	out := SessionUsage{
		SessionID:       sessionID,
		Model:           t.Model,
		ContextWindow:   window,
		ContextKnown:    known,
		Counters:        t.Counters,
		LastInputTokens: t.LastInputTokens,
		ContextTokens:   contextTokens,
		FillPercent:     fillPercent(used, window, known),
	}
	// Surface the inferred flag so the client labels a subscription's
	// list price as an estimate even when the ledger predates the field.
	out.CostEstimated = out.Estimated()
	for name, m := range t.Models {
		if m == nil {
			continue
		}
		mv := ModelUsageView{Model: name, Counters: m.Counters, LastInputTokens: m.LastInputTokens}
		mv.CostEstimated = mv.Estimated()
		switch {
		case m.ContextWindow > 0:
			mv.ContextWindow, mv.ContextKnown, mv.ContextTokens = m.ContextWindow, true, m.ContextTokens
			mv.FillPercent = fillPercent(m.ContextTokens, m.ContextWindow, true)
		case windowOf != nil:
			if w, ok := windowOf(name); ok {
				mv.ContextWindow, mv.ContextKnown = w, true
				mv.FillPercent = fillPercent(m.LastInputTokens, w, true)
			}
		}
		out.Models = append(out.Models, mv)
	}
	SortModelsHeaviestFirst(out.Models)
	out.Daily = DailyPoints(t.Daily)
	for i := range out.Daily {
		out.Daily[i].CostEstimated = out.Daily[i].Estimated()
	}
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
