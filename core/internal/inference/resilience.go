package inference

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
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
			retryAfter := pe.RetryAfter
			if retryAfter == 0 {
				retryAfter = parseRetryHint(pe.Message)
			}
			return RetryDecision{Retryable: retryableStatus(pe.Status), RetryAfter: retryAfter}
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

// retryHintRe finds a wait hint embedded in an error message, used by
// gateways that omit Retry-After. OpenCode Go, for example, refuses a
// request at capacity with 429 and the wording
// "retry shortly (after 30 s)".
var retryHintRe = regexp.MustCompile(`(?i)\b(?:retry(?:ing)?|try again|try|wait)\b[^0-9]{0,40}?(\d+(?:\.\d+)?)\s*(ms|milliseconds?|seconds?|secs?|minutes?|mins?|s|m)\b`)

// parseRetryHint reads the server-requested wait from error text such as
// "retry shortly (after 30 s)", "retry after 30 seconds" or "try again
// in 30s". It returns 0 when the text carries no such hint.
func parseRetryHint(msg string) time.Duration {
	m := retryHintRe.FindStringSubmatch(msg)
	if m == nil {
		return 0
	}
	n, err := strconv.ParseFloat(m[1], 64)
	if err != nil || n <= 0 {
		return 0
	}
	unit := strings.ToLower(m[2])
	switch {
	case strings.HasPrefix(unit, "ms"), strings.HasPrefix(unit, "milli"):
		return time.Duration(n * float64(time.Millisecond))
	case strings.HasPrefix(unit, "m"):
		return time.Duration(n * float64(time.Minute))
	default:
		return time.Duration(n * float64(time.Second))
	}
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
	retryAfter := parseRetryAfter(resp.Header, time.Now())
	if retryAfter == 0 {
		retryAfter = parseRetryHint(body)
	}
	return &ProviderError{Kind: "request", Message: msg, Status: resp.StatusCode, RetryAfter: retryAfter}
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

// throttlePhrases mark an error as rate limiting rather than an
// over-long prompt, even when the wording mentions tokens: a TPM
// throttle ("too many tokens per minute") must back off and retry, not
// compact. Mirrors opencode's exclusion of throttling codes.
var throttlePhrases = []string{
	"per minute",
	"rate limit",
	"ratelimit",
	"too many requests",
	"try again later",
	"please wait",
	"throttl",
	"quota",
	"billing",
	"credit",
	"insufficient",
}

// IsContextOverflow reports whether err is the provider rejecting the
// request because the prompt doesn't fit the model's context window.
// Overflow wording wins on any status — some gateways surface it as
// 500/529 — except throttling (429 or throttle wording), which must
// back off and retry instead of compacting.
func IsContextOverflow(err error) bool {
	if err == nil || IsCancelled(err) {
		return false
	}
	var pe *ProviderError
	if errors.As(err, &pe) {
		switch pe.Status {
		case http.StatusRequestEntityTooLarge:
			return true
		case http.StatusTooManyRequests:
			return false
		}
	}
	msg := strings.ToLower(err.Error())
	for _, p := range throttlePhrases {
		if strings.Contains(msg, p) {
			return false
		}
	}
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
