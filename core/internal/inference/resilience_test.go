package inference

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		h    http.Header
		want time.Duration
	}{
		{http.Header{"Retry-After": {"7"}}, 7 * time.Second},
		{http.Header{"Retry-After-Ms": {"1500"}}, 1500 * time.Millisecond},
		{http.Header{"Retry-After": {now.Add(20 * time.Second).Format(http.TimeFormat)}}, 20 * time.Second},
		{http.Header{"Retry-After": {"garbage"}}, 0},
		{http.Header{"Retry-After": {"-3"}}, 0},
		{nil, 0},
	}
	for _, c := range cases {
		if got := parseRetryAfter(c.h, now); got != c.want {
			t.Fatalf("%v: got %v want %v", c.h, got, c.want)
		}
	}
}

func TestParseRetryHint(t *testing.T) {
	cases := []struct {
		msg  string
		want time.Duration
	}{
		{`Upstream request failed: [prefill_cap_refused] the cell is at capacity; retry shortly (after 30 s)`, 30 * time.Second},
		{`{"type":"server_error","message":"Upstream request failed: [ttft_admission_refused] the cell is at capacity; retry shortly (after 42 s)"}`, 42 * time.Second},
		{"rate limited: retry after 30 seconds", 30 * time.Second},
		{"gateway is retrying in 12s", 12 * time.Second},
		{"try again in 1.5s", 1500 * time.Millisecond},
		{"please wait 2 minutes", 2 * time.Minute},
		{"retry after 250 ms", 250 * time.Millisecond},
		{"retry in 0s", 0},
		{"status 429: slow down", 0},
		{"the model is overloaded", 0},
		{"", 0},
	}
	for _, c := range cases {
		if got := parseRetryHint(c.msg); got != c.want {
			t.Errorf("%q: got %v want %v", c.msg, got, c.want)
		}
	}
}

func TestStreamCarriesStatusAndRetryAfterWithoutHiddenRetries(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Retry-After", "7")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":{"message":"slow down"}}`)
	}))
	defer srv.Close()
	err := providerFor(srv.URL).StreamTools(context.Background(), "sys", []LlmMessage{{Role: "user", Content: "hi"}}, nil, "s", make(chan Chunk, 8), make(chan struct{}))
	var pe *ProviderError
	if !errors.As(err, &pe) {
		t.Fatalf("got %T %v", err, err)
	}
	if pe.Status != 429 || pe.RetryAfter != 7*time.Second {
		t.Fatalf("want status 429 retry-after 7s, got %+v", pe)
	}
	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Fatalf("the agent owns retries; SDK made %d attempts", n)
	}
	d := Classify(err)
	if !d.Retryable || d.RetryAfter != 7*time.Second || d.Timeout {
		t.Fatalf("classify: %+v", d)
	}
}

func TestStreamParsesRetryHintFromBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":{"type":"server_error","message":"Upstream request failed: [prefill_cap_refused] the cell is at capacity; retry shortly (after 30 s)"}}`)
	}))
	defer srv.Close()
	err := providerFor(srv.URL).StreamTools(context.Background(), "sys", []LlmMessage{{Role: "user", Content: "hi"}}, nil, "s", make(chan Chunk, 8), make(chan struct{}))
	var pe *ProviderError
	if !errors.As(err, &pe) {
		t.Fatalf("got %T %v", err, err)
	}
	if pe.Status != 429 || pe.RetryAfter != 30*time.Second {
		t.Fatalf("want status 429 retry-after 30s from the message, got %+v", pe)
	}
	d := Classify(err)
	if !d.Retryable || d.RetryAfter != 30*time.Second || d.Timeout {
		t.Fatalf("classify: %+v", d)
	}
}

func TestClassifyParsesRetryHintFromMessage(t *testing.T) {
	err := &ProviderError{Kind: "request", Status: 429,
		Message: `p: stream: status 429: {"message":"at capacity; retry shortly (after 12 s)"}`}
	d := Classify(err)
	if !d.Retryable || d.RetryAfter != 12*time.Second {
		t.Fatalf("classify: %+v", d)
	}
}

func withIdleTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	old := streamIdleTimeout
	streamIdleTimeout = d
	t.Cleanup(func() { streamIdleTimeout = old })
}

func TestStreamIdleWatchdogFailsStalledStream(t *testing.T) {
	withIdleTimeout(t, 150*time.Millisecond)
	release := make(chan struct{})
	defer close(release)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	start := time.Now()
	err := providerFor(srv.URL).StreamTools(context.Background(), "sys", []LlmMessage{{Role: "user", Content: "hi"}}, nil, "s", make(chan Chunk, 8), make(chan struct{}))
	if time.Since(start) > 3*time.Second {
		t.Fatal("watchdog did not fire")
	}
	d := Classify(err)
	if !d.Timeout || !d.Retryable {
		t.Fatalf("an idle stall is a retryable timeout, got %+v (%v)", d, err)
	}
}

func TestStreamIdleWatchdogAllowsSlowSteadyStream(t *testing.T) {
	withIdleTimeout(t, 150*time.Millisecond)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for i := 0; i < 6; i++ { // ~480ms total, well past the idle window
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"%d\"}}]}\n\n", i)
			w.(http.Flusher).Flush()
			time.Sleep(80 * time.Millisecond)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()
	tx := make(chan Chunk, 32)
	if err := providerFor(srv.URL).StreamTools(context.Background(), "sys", []LlmMessage{{Role: "user", Content: "hi"}}, nil, "s", tx, make(chan struct{})); err != nil {
		t.Fatalf("steady stream killed: %v", err)
	}
}

func TestAnthropicStreamIdleWatchdogAndRetryAfter(t *testing.T) {
	withIdleTimeout(t, 150*time.Millisecond)
	release := make(chan struct{})
	defer close(release)
	stall := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer stall.Close()
	err := anthropicProvider(t, stall).StreamTools(context.Background(), "sys", []LlmMessage{{Role: "user", Content: "hi"}}, nil, "s", make(chan Chunk, 8), make(chan struct{}))
	if d := Classify(err); !d.Timeout {
		t.Fatalf("anthropic stall: %+v (%v)", d, err)
	}
	limited := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "3")
		w.WriteHeader(529)
		fmt.Fprint(w, `{"error":{"message":"overloaded"}}`)
	}))
	defer limited.Close()
	err = anthropicProvider(t, limited).StreamTools(context.Background(), "sys", []LlmMessage{{Role: "user", Content: "hi"}}, nil, "s", make(chan Chunk, 8), make(chan struct{}))
	if d := Classify(err); !d.Retryable || d.RetryAfter != 3*time.Second {
		t.Fatalf("anthropic 529: %+v (%v)", d, err)
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		err            error
		retry, timeout bool
	}{
		{&ProviderError{Kind: "request", Status: 503, Message: "x"}, true, false},
		{&ProviderError{Kind: "request", Status: 408, Message: "x"}, true, false},
		{&ProviderError{Kind: "request", Status: 400, Message: "x"}, false, false},
		{&ProviderError{Kind: "request", Status: 401, Message: "x"}, false, false},
		{&ProviderError{Kind: "request", Timeout: true, Message: "idle"}, true, true},
		{RequestError("x: stream: status 502"), true, false},
		{RequestError("x: transient connection (EOF)"), true, false},
		{CancelledError(), false, false},
		{nil, false, false},
	}
	for _, c := range cases {
		d := Classify(c.err)
		if d.Retryable != c.retry || d.Timeout != c.timeout {
			t.Fatalf("%v: got %+v", c.err, d)
		}
	}
}

func TestIsContextOverflow(t *testing.T) {
	yes := []error{
		&ProviderError{Kind: "request", Status: 400, Message: "prompt is too long: 210000 tokens > 200000 maximum"},
		&ProviderError{Kind: "request", Status: 400, Message: "This model's maximum context length is 128000 tokens"},
		&ProviderError{Kind: "request", Status: 400, Message: `{"code":"context_length_exceeded"}`},
		&ProviderError{Kind: "request", Status: 413, Message: "request entity too large"},
		errors.New("input exceeds the context window of this model"),
		&ProviderError{Kind: "request", Status: 400, Message: "The input token count (1200000) exceeds the maximum number of tokens allowed"},
		&ProviderError{Kind: "request", Status: 500, Message: "context length unknown: prompt too large for gateway buffer"},
		&ProviderError{Kind: "request", Status: 529, Message: "prompt is too long: reduce the length and try again"},
	}
	for _, err := range yes {
		if !IsContextOverflow(err) {
			t.Errorf("missed overflow: %v", err)
		}
	}
	no := []error{
		nil,
		&ProviderError{Kind: "request", Status: 429, Message: "rate limit: too many tokens per minute"},
		&ProviderError{Kind: "request", Status: 500, Message: "tokens per minute quota exceeded, try again later"},
		&ProviderError{Kind: "request", Status: 400, Message: "invalid tool schema"},
		CancelledError(),
	}
	for _, err := range no {
		if IsContextOverflow(err) {
			t.Errorf("false positive: %v", err)
		}
	}
}

func TestReadBodyIsBounded(t *testing.T) {
	data, err := readBody(strings.NewReader("0123456789"), 10)
	if err != nil || string(data) != "0123456789" {
		t.Fatalf("at the limit: %q, %v", data, err)
	}
	if _, err := readBody(strings.NewReader("0123456789x"), 10); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("over the limit: err = %v", err)
	}
	if got := readErrorBody(strings.NewReader(strings.Repeat("e", maxErrorBody+100))); len(got) != maxErrorBody {
		t.Fatalf("error body kept %d bytes, want %d", len(got), maxErrorBody)
	}
}
