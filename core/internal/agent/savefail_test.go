// savefail_test.go ensures a turn does not start when its journal cannot
// be saved, so it cannot make unrecorded changes.

package agent

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestTurnDoesNotRunWithoutDurableJournal(t *testing.T) {
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
		t.Fatal("want the journal failure")
	}
	if !strings.Contains(err.Error(), "start turn journal") || prov.calls != 0 {
		t.Fatalf("turn ran without a journal: calls=%d error=%v", prov.calls, err)
	}
}
