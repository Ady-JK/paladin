package inference

import (
	"errors"
	"testing"
)

func TestIsRetryable(t *testing.T) {
	retry := []error{
		RequestError("opencode: stream: status 429"),
		RequestError("opencode: stream: status 503 Service Unavailable"),
		RequestError("opencode: responses: status 529"),
		RequestError("opencode: responses: transient connection (EOF) — try again"),
		RequestError("opencode: stream: Post https://x/v1/responses: EOF"),
		errors.New("connection reset by peer"),
	}
	for _, err := range retry {
		if !IsRetryable(err) {
			t.Fatalf("expected retryable: %v", err)
		}
	}
	fatal := []error{
		nil,
		CancelledError(),
		ConfigError("no key"),
		RequestError("opencode: stream: status 401 unauthorized"),
		RequestError("opencode: stream: status 400 invalid request"),
		RequestError("opencode: responses: context window exceeded"),
		RequestError("opencode: stream: model response timed out after 5m0s — still working"),
		RequestError("opencode: chat completion: quota exceeded"),
		errors.New("some other failure"),
	}
	for _, err := range fatal {
		if IsRetryable(err) {
			t.Fatalf("expected fatal: %v", err)
		}
	}
}
