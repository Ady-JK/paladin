package codex

import "context"

// RateWindow is one of the plan's usage windows (the last five hours, the
// week). ResetsAt is unix seconds, 0 when Codex did not say.
type RateWindow struct {
	UsedPercent  float64
	DurationMins int64
	ResetsAt     int64
}

// RateLimits is how much of the account's plan is used.
type RateLimits struct {
	Plan      string
	Primary   *RateWindow
	Secondary *RateWindow
}

// RateLimits reads the account's usage limits. Codex keeps a snapshot per kind
// of limit and the main one is "codex", which wins over the summary snapshot.
func (c *Client) RateLimits(ctx context.Context) (RateLimits, error) {
	var res struct {
		RateLimits   Raw            `json:"rateLimits"`
		ByLimitID    map[string]Raw `json:"rateLimitsByLimitId"`
		AccountError any            `json:"-"`
	}
	if err := c.Call(ctx, "account/rateLimits/read", nil, &res); err != nil {
		return RateLimits{}, err
	}
	snapshot := res.RateLimits
	if main, ok := res.ByLimitID["codex"]; ok {
		snapshot = main
	}
	return RateLimits{Plan: snapshot.Str("planType"), Primary: windowOf(snapshot.Obj("primary")), Secondary: windowOf(snapshot.Obj("secondary"))}, nil
}

func windowOf(w Raw) *RateWindow {
	if len(w) == 0 || !w.Has("usedPercent") {
		return nil
	}
	return &RateWindow{UsedPercent: float64(w.Int("usedPercent")), DurationMins: w.Int("windowDurationMins"), ResetsAt: w.Int("resetsAt")}
}
