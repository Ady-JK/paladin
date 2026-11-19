package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

// A failed call keeps its reason in the result the app shows. A job that
// failed reports its state as JSON with no "error" key; the reason must
// still reach the result.
func TestFailedResultCarriesReason(t *testing.T) {
	err := WithContent(`{"job_id":"job-15","status":"failed","exit_code":2}`, "shell: job job-15 failed")
	var got map[string]any
	if jerr := json.Unmarshal([]byte(err.ResultContent()), &got); jerr != nil {
		t.Fatalf("result is not JSON: %v", jerr)
	}
	if got["error"] != "shell: job job-15 failed" {
		t.Fatalf("error = %v, want the reason", got["error"])
	}
	if got["job_id"] != "job-15" || got["exit_code"] != float64(2) {
		t.Fatalf("job fields lost: %v", got)
	}
}

func TestFailedResultKeepsExistingError(t *testing.T) {
	err := WithContent(`{"error":"from the tool"}`, "something else")
	if got := err.ResultContent(); got != `{"error":"from the tool"}` {
		t.Fatalf("existing error changed: %s", got)
	}
}

func TestFailedTextResultIsUnchanged(t *testing.T) {
	err := WithContent("plain output", "shell: job job-1 failed")
	if got := err.ResultContent(); got != "plain output" {
		t.Fatalf("text result changed: %q", got)
	}
	if !strings.Contains(ArgsError("bad").ResultContent(), `"error":"bad"`) {
		t.Fatalf("argument errors lost their error field")
	}
}

func TestFailedEmptyResultCarriesReason(t *testing.T) {
	if got := WithContent("", "shell: job job-2 failed").ResultContent(); got != `{"error":"shell: job job-2 failed"}` {
		t.Fatalf("empty failure has no reason: %s", got)
	}
}
