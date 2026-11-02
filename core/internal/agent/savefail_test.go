// savefail_test.go covers the double-fault path: when a turn fails
// AND its transcript cannot persist, the returned error must mention
// both instead of silently dropping the whole turn.

package agent

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestFailedTurnWithBrokenPersistReportsBoth(t *testing.T) {
	prov := &mockProvider{streamsErr: errors.New("provider exploded")}
	sess := testSession(t, "00000000-0000-4000-8000-000000000041")
	// Remove the session dir so the transcript append cannot succeed.
	if err := os.RemoveAll(sess.Dir); err != nil {
		t.Fatal(err)
	}
	a, _, _, done := testAgent(t, prov)
	t.Cleanup(func() { close(done) })
	// Point the agent at the broken session dir.
	a.session = sess
	_, err := a.Run()
	if err == nil {
		t.Fatal("want the provider failure")
	}
	if !strings.Contains(err.Error(), "provider exploded") {
		t.Fatalf("original error lost: %v", err)
	}
	if !strings.Contains(err.Error(), "could not be saved") {
		t.Fatalf("persist failure swallowed: %v", err)
	}
}
