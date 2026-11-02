// plainempty_test.go covers the no-tools completion path: a stream and
// buffered fallback that yield nothing must fail loudly, like the
// tool path's empty-round guard, instead of ending as a silent READY.

package agent

import (
	"strings"
	"testing"
)

func TestRunPlainEmptyFailsLoudly(t *testing.T) {
	prov := &mockProvider{
		streams: []streamScript{{}},
	}
	sess := testSession(t, "00000000-0000-4000-8000-000000000040")
	a, _, _ := newAgentForTest(t, prov, sess, "hi", false, false)
	_, err := a.Run()
	if err == nil {
		t.Fatal("empty plain turn ended silently")
	}
	if terr, ok := err.(turnFailed); !ok || !strings.Contains(terr.msg, "empty response") {
		t.Fatalf("got %T %v", err, err)
	}
}
