package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/Ady-JK/paladin/core/internal/inference"
)

func fastRetries(t *testing.T) {
	t.Helper()
	old := retryUnit
	retryUnit = time.Millisecond
	t.Cleanup(func() { retryUnit = old })
}

func retryChunks(chunks []ChatChunk) []RetryStatus {
	var out []RetryStatus
	for _, c := range chunks {
		if c.Retry != nil {
			out = append(out, *c.Retry)
		}
	}
	return out
}

func TestRetryHonorsRetryAfterAndReportsStatus(t *testing.T) {
	fastRetries(t)
	limited := &inference.ProviderError{Kind: "request", Status: 429, RetryAfter: 150 * time.Millisecond, Message: "p: stream: status 429: slow down"}
	prov := &mockProvider{streams: []streamScript{
		{err: limited},
		{chunks: []inference.Chunk{inference.TextChunk("ok")}},
	}}
	a, _, tx, done := testAgent(t, prov)
	defer close(done)
	start := time.Now()
	outcome, err := a.Run()
	if err != nil || outcome.Reply != "ok" {
		t.Fatalf("got %+v %v", outcome, err)
	}
	if time.Since(start) < 150*time.Millisecond {
		t.Fatal("Retry-After was not honored")
	}
	rs := retryChunks(drainChunks(tx))
	if len(rs) != 1 {
		t.Fatalf("want one retry status chunk, got %+v", rs)
	}
	if rs[0].Attempt != 1 || rs[0].Max != maxRetries || rs[0].DelayMs < 150 || !strings.Contains(rs[0].Reason, "429") {
		t.Fatalf("retry status %+v", rs[0])
	}
}

func TestRetryBudgetIsSixAttempts(t *testing.T) {
	fastRetries(t)
	prov := &mockProvider{streamsErr: inference.RequestError("p: stream: status 503")}
	a, _, _, done := testAgent(t, prov)
	defer close(done)
	if _, err := a.Run(); err == nil {
		t.Fatal("persistent 503 must fail the turn")
	}
	if prov.calls != 1+maxRetries {
		t.Fatalf("want %d stream calls, got %d", 1+maxRetries, prov.calls)
	}
}

func TestTimeoutRetriesAreLimited(t *testing.T) {
	fastRetries(t)
	prov := &mockProvider{streamsErr: &inference.ProviderError{Kind: "request", Timeout: true, Message: "p: stream: stream stalled"}}
	a, _, _, done := testAgent(t, prov)
	defer close(done)
	if _, err := a.Run(); err == nil {
		t.Fatal("persistent stall must fail the turn")
	}
	if prov.calls != 1+maxTimeoutRetries {
		t.Fatalf("want %d stream calls, got %d", 1+maxTimeoutRetries, prov.calls)
	}
}

func TestRetryAfterBeyondCapFailsFast(t *testing.T) {
	fastRetries(t)
	prov := &mockProvider{streamsErr: &inference.ProviderError{Kind: "request", Status: 429, RetryAfter: 2 * time.Hour, Message: "p: stream: status 429: daily quota"}}
	a, _, _, done := testAgent(t, prov)
	defer close(done)
	start := time.Now()
	_, err := a.Run()
	if err == nil || prov.calls != 1 || time.Since(start) > time.Second {
		t.Fatalf("an hours-long Retry-After must fail fast, got err=%v calls=%d", err, prov.calls)
	}
}

func TestRetriesDoNotConsumeRounds(t *testing.T) {
	fastRetries(t)
	prov := &mockProvider{streams: []streamScript{
		{err: inference.RequestError("p: stream: status 503")},
		{err: inference.RequestError("p: stream: status 503")},
		{chunks: []inference.Chunk{inference.TextChunk("ok")}},
	}}
	a, _, _, done := testAgent(t, prov)
	defer close(done)
	outcome, err := a.Run()
	if err != nil || outcome.Reply != "ok" {
		t.Fatalf("retries burned the round budget: %+v %v", outcome, err)
	}
}

func TestStopDuringBackoffReturnsPromptly(t *testing.T) {
	old := retryUnit
	retryUnit = time.Second
	defer func() { retryUnit = old }()
	prov := &mockProvider{streamsErr: inference.RequestError("p: stream: status 503")}
	a, _, _, done := testAgent(t, prov)
	defer close(done)
	go func() {
		time.Sleep(50 * time.Millisecond)
		a.cancel()
	}()
	start := time.Now()
	outcome, err := a.Run()
	if time.Since(start) > 500*time.Millisecond {
		t.Fatal("stop did not interrupt the backoff")
	}
	if err != nil || !outcome.Stopped {
		t.Fatalf("stop during backoff must end as stopped: %+v %v", outcome, err)
	}
}

func TestBackoffGrowsAndCaps(t *testing.T) {
	prev := time.Duration(0)
	for n := 0; n < 4; n++ {
		d := backoff(n)
		lo := time.Duration(float64(2*retryUnit<<n) * 0.8)
		if d < lo || d < prev/2 {
			t.Fatalf("attempt %d: backoff %v too small", n, d)
		}
		prev = d
	}
	for n := 0; n < 20; n++ {
		if d := backoff(n); d > time.Duration(float64(maxBackoff())*1.2) {
			t.Fatalf("attempt %d: backoff %v exceeds cap", n, d)
		}
	}
}
