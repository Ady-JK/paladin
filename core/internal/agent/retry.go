package agent

import (
	"math/rand/v2"
	"strings"
	"time"

	"github.com/Ady-JK/paladin/core/internal/inference"
)

// retryUnit is the backoff quantum; tests shrink it.
var retryUnit = time.Second

const (
	// maxRetries bounds retries per stretch of no progress.
	maxRetries = 6
	// maxTimeoutRetries bounds stalls/timeouts within that budget:
	// resending a request the model is choking on rarely helps twice.
	maxTimeoutRetries = 2
	// maxRetryAfter is the longest server-requested wait worth sitting
	// through; beyond it (daily quotas) the turn fails with the error.
	maxRetryAfter = 5 * time.Minute
)

// maxBackoff caps the exponential backoff (30s in production).
func maxBackoff() time.Duration { return 30 * retryUnit }

// RetryStatus tells the UI a failed round is being retried.
type RetryStatus struct {
	Attempt int    `json:"attempt"`
	Max     int    `json:"max"`
	DelayMs int64  `json:"delayMs"`
	Reason  string `json:"reason"`
}

// backoff is 2u, 4u, 8u... capped at maxBackoff, with ±20% jitter so
// parallel sessions don't retry in lockstep.
func backoff(attempt int) time.Duration {
	if attempt > 20 {
		attempt = 20
	}
	d := 2 * retryUnit << attempt
	if d > maxBackoff() {
		d = maxBackoff()
	}
	return time.Duration(float64(d) * (0.8 + 0.4*rand.Float64()))
}

// retryRound decides whether a failed round runs again and sleeps for
// the backoff (honoring Retry-After). Only rounds with zero progress
// retry: re-issuing a partially streamed round would duplicate visible
// output. Any progress resets the budget (see runLoop). A retry is not
// a new round, so it doesn't consume the user's round budget.
func (a *Agent) retryRound(err error, noProgress bool) bool {
	if !noProgress || a.retries >= maxRetries {
		return false
	}
	d := inference.Classify(err)
	if !d.Retryable || d.RetryAfter > maxRetryAfter {
		return false
	}
	if d.Timeout {
		if a.timeoutRetries >= maxTimeoutRetries {
			return false
		}
		a.timeoutRetries++
	}
	wait := backoff(a.retries)
	if d.RetryAfter > wait {
		wait = d.RetryAfter
	}
	a.retries++
	_ = a.emit(ChatChunk{Retry: &RetryStatus{
		Attempt: a.retries, Max: maxRetries,
		DelayMs: wait.Milliseconds(), Reason: retryReason(err),
	}})
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-timer.C:
		if a.round > 0 {
			a.round--
		}
		return true
	case <-a.ctx.Done():
		return false
	case <-a.done:
		return false
	}
}

func retryReason(err error) string {
	r := []rune(strings.TrimSpace(err.Error()))
	if len(r) > 200 {
		r = append(r[:200], '…')
	}
	return string(r)
}
