package inference

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// streamIdleTimeout bounds the silence between stream events, not the
// whole response: a long but steadily streaming generation is fine, a
// stalled connection fails fast. Tests shrink it.
var streamIdleTimeout = 300 * time.Second

// RetryDecision is the agent-facing classification of a failed call.
type RetryDecision struct {
	Retryable  bool
	Timeout    bool
	RetryAfter time.Duration
}

// Classify decides whether a provider failure is worth another attempt.
// Structured status codes win; untyped errors fall back to IsRetryable's
// message heuristics.
func Classify(err error) RetryDecision {
	if err == nil || IsCancelled(err) {
		return RetryDecision{}
	}
	var pe *ProviderError
	if errors.As(err, &pe) {
		if pe.Kind != "request" {
			return RetryDecision{}
		}
		if pe.Timeout {
			return RetryDecision{Retryable: true, Timeout: true, RetryAfter: pe.RetryAfter}
		}
		if pe.Status != 0 {
			return RetryDecision{Retryable: retryableStatus(pe.Status), RetryAfter: pe.RetryAfter}
		}
	}
	return RetryDecision{Retryable: IsRetryable(err)}
}

func retryableStatus(status int) bool {
	switch {
	case status == http.StatusRequestTimeout, status == http.StatusConflict,
		status == http.StatusTooEarly, status == http.StatusTooManyRequests:
		return true
	case status >= 500:
		return true
	}
	return false
}

// parseRetryAfter reads retry-after-ms (OpenAI/Anthropic) or the
// standard Retry-After in seconds or HTTP-date form.
func parseRetryAfter(h http.Header, now time.Time) time.Duration {
	if h == nil {
		return 0
	}
	if v := strings.TrimSpace(h.Get("Retry-After-Ms")); v != "" {
		if ms, err := strconv.ParseFloat(v, 64); err == nil && ms > 0 {
			return time.Duration(ms * float64(time.Millisecond))
		}
	}
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return 0
	}
	if secs, err := strconv.ParseFloat(v, 64); err == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(secs * float64(time.Second))
	}
	if at, err := http.ParseTime(v); err == nil {
		if d := at.Sub(now); d > 0 {
			return d
		}
	}
	return 0
}

// statusErrorFrom builds a status-coded error from an HTTP response.
func statusErrorFrom(name, op string, resp *http.Response, body string) *ProviderError {
	short := []rune(strings.TrimSpace(body))
	if len(short) > 300 {
		short = short[:300]
	}
	msg := fmt.Sprintf("%s: %s: status %d", name, op, resp.StatusCode)
	if len(short) > 0 {
		msg += ": " + string(short)
	}
	return &ProviderError{Kind: "request", Message: msg, Status: resp.StatusCode, RetryAfter: parseRetryAfter(resp.Header, time.Now())}
}

// idleWatchdog cancels a stream's context after streamIdleTimeout of
// silence; touch() on every event re-arms it.
type idleWatchdog struct {
	timer *time.Timer
	d     time.Duration
	fired atomic.Bool
}

func watchIdle(parent context.Context) (context.Context, *idleWatchdog, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	w := &idleWatchdog{d: streamIdleTimeout}
	w.timer = time.AfterFunc(w.d, func() {
		w.fired.Store(true)
		cancel()
	})
	return ctx, w, func() {
		w.timer.Stop()
		cancel()
	}
}

func (w *idleWatchdog) touch() { w.timer.Reset(w.d) }

func (w *idleWatchdog) Fired() bool { return w.fired.Load() }

func idleError(name, op string) *ProviderError {
	return &ProviderError{Kind: "request", Timeout: true,
		Message: fmt.Sprintf("%s: %s: stream stalled — no data from the model for %s", name, op, streamIdleTimeout)}
}

// overflowPhrases are provider wordings for "the prompt exceeds the
// model's context window" (OpenAI, Anthropic, Gemini, OpenRouter,
// LiteLLM, vLLM/llama.cpp gateways).
var overflowPhrases = []string{
	"context length",
	"context_length_exceeded",
	"context window",
	"maximum context",
	"prompt is too long",
	"input is too long",
	"too many tokens",
	"exceeds the maximum number of tokens",
	"maximum number of tokens allowed",
	"input token count",
	"request too large",
	"reduce the length",
}

// IsContextOverflow reports whether err is the provider rejecting the
// request because the prompt doesn't fit the model's context window.
// Only client errors (400/413/422) or untyped errors qualify: a rate
// limit about tokens-per-minute is not an overflow.
func IsContextOverflow(err error) bool {
	if err == nil || IsCancelled(err) {
		return false
	}
	var pe *ProviderError
	if errors.As(err, &pe) {
		switch pe.Status {
		case 0, http.StatusBadRequest, http.StatusUnprocessableEntity:
		case http.StatusRequestEntityTooLarge:
			return true
		default:
			return false
		}
	}
	msg := strings.ToLower(err.Error())
	for _, p := range overflowPhrases {
		if strings.Contains(msg, p) {
			return true
		}
	}
	return false
}

const (
	// maxResponseBody bounds a non-streaming reply or model listing.
	maxResponseBody = 64 << 20
	// maxErrorBody is as much of an error reply as is worth reporting.
	maxErrorBody = 64 << 10
)

// readBody reads all of r, failing (not truncating) past limit bytes so
// a runaway reply can't exhaust memory or parse as a cut-off document.
func readBody(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("response larger than %d bytes", limit)
	}
	return data, nil
}

// readErrorBody returns the start of an error reply; read failures just
// shorten it, since the status code is the real error.
func readErrorBody(r io.Reader) string {
	data, _ := io.ReadAll(io.LimitReader(r, maxErrorBody))
	return string(data)
}
