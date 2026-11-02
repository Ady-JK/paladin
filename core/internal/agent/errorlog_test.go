// errorlog_test.go pins the debugging trail: a failed turn and every
// failed tool call land raw in the session's error.log, so a bug can be
// read back later from what actually happened.

package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/errlog"
	"github.com/Ady-JK/paladin/core/internal/inference"
)

func readErrorLog(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, errlog.FileName))
	if err != nil {
		t.Fatalf("no error.log: %v", err)
	}
	return string(data)
}

func TestFailedTurnIsLoggedToSession(t *testing.T) {
	prov := &mockProvider{
		streams: []streamScript{{err: inference.RequestError("Opencode Go: stream: status 401 unauthorized")}},
	}
	a, sess, _, done := testAgent(t, prov)
	defer close(done)
	if _, err := a.Run(); err == nil {
		t.Fatal("want the turn to fail")
	}
	log := readErrorLog(t, sess.Dir)
	if !strings.Contains(log, "\tturn\t") {
		t.Fatalf("turn failure not tagged: %q", log)
	}
	if !strings.Contains(log, "Opencode Go: stream: status 401 unauthorized") {
		t.Fatalf("raw provider error missing: %q", log)
	}
}

func TestToolFailureIsLoggedToSession(t *testing.T) {
	prov := &mockProvider{streams: []streamScript{
		callRound("c1", "read", `{"path":"/definitely/not/here.txt"}`),
		textRound("done"),
	}}
	a, sess, _, done := testAgent(t, prov)
	defer close(done)
	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	log := readErrorLog(t, sess.Dir)
	if !strings.Contains(log, "\ttool:read\t") {
		t.Fatalf("tool failure not tagged: %q", log)
	}
	if !strings.Contains(log, "c1") || !strings.Contains(log, "/definitely/not/here.txt") {
		t.Fatalf("tool call context missing: %q", log)
	}
}

func TestRetriedRoundIsLogged(t *testing.T) {
	old := retryUnit
	retryUnit = 0
	t.Cleanup(func() { retryUnit = old })
	prov := &mockProvider{streams: []streamScript{
		{err: inference.RequestError("Opencode Go: stream: unexpected end of JSON input")},
		textRound("recovered"),
	}}
	a, sess, _, done := testAgent(t, prov)
	defer close(done)
	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	log := readErrorLog(t, sess.Dir)
	if !strings.Contains(log, "\tretry\t") {
		t.Fatalf("retry not logged: %q", log)
	}
	if !strings.Contains(log, "unexpected end of JSON input") {
		t.Fatalf("raw retry reason missing: %q", log)
	}
}

// TestCleanTurnWritesNoLog keeps the noise out: a turn with no errors
// leaves no file at all.
func TestCleanTurnWritesNoLog(t *testing.T) {
	prov := &mockProvider{streams: []streamScript{textRound("all good")}}
	a, sess, _, done := testAgent(t, prov)
	defer close(done)
	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(sess.Dir, errlog.FileName)); !os.IsNotExist(err) {
		t.Fatalf("a clean turn must not create error.log: %v", err)
	}
}
