package runtime

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/Ady-JK/paladin/core/internal/codex"
	"github.com/Ady-JK/paladin/core/internal/session"
)

const (
	codexLimitsTTL     = time.Minute
	codexLimitsTimeout = 3 * time.Second
)

// codexLimitCache keeps the account's plan limits between reads: they change
// slowly, and the usage view is asked for often.
type codexLimitCache struct {
	mu      sync.Mutex
	at      time.Time
	windows []session.LimitWindow
}

// limitWindows names a snapshot's windows by their length. Without one, a paid
// plan's windows are five hours and a week, and a free plan's a month.
func limitWindows(l codex.RateLimits) []session.LimitWindow {
	monthly := strings.EqualFold(l.Plan, "free") || strings.EqualFold(l.Plan, "go")
	primaryDefault := int64(300)
	if monthly {
		primaryDefault = 30 * 24 * 60
	}
	var out []session.LimitWindow
	add := func(w *codex.RateWindow, fallback int64) {
		if w == nil {
			return
		}
		mins := w.DurationMins
		if mins <= 0 {
			mins = fallback
		}
		label := "Session"
		switch {
		case mins >= 30*24*60:
			label = "Monthly"
		case mins >= 7*24*60:
			label = "Weekly"
		}
		used := min(max(w.UsedPercent, 0), 100)
		resets := w.ResetsAt
		if resets < 0 {
			resets = 0
		}
		out = append(out, session.LimitWindow{Label: label, UsedPercent: used, ResetsAt: resets})
	}
	add(l.Primary, primaryDefault)
	add(l.Secondary, 7*24*60)
	return out
}

// refreshCodexLimits reads the plan's limits from a running Codex.
func (r *AgentRuntime) refreshCodexLimits(client *codex.Client) {
	if client == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), codexLimitsTimeout)
	defer cancel()
	limits, err := client.RateLimits(ctx)
	if err != nil {
		return // an API-key account has none; the old reading stands
	}
	r.codexLimits.mu.Lock()
	r.codexLimits.windows, r.codexLimits.at = limitWindows(limits), time.Now()
	r.codexLimits.mu.Unlock()
}

// codexLimitWindows is the plan's limits for the usage view. It never starts
// Codex: with it not running, the last reading is all there is.
func (r *AgentRuntime) codexLimitWindows() []session.LimitWindow {
	r.codexLimits.mu.Lock()
	stale := time.Since(r.codexLimits.at) > codexLimitsTTL
	r.codexLimits.mu.Unlock()
	if stale {
		r.refreshCodexLimits(r.codex.Live())
	}
	r.codexLimits.mu.Lock()
	defer r.codexLimits.mu.Unlock()
	return append([]session.LimitWindow(nil), r.codexLimits.windows...)
}
