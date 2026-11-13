// plainempty_test.go covers the no-tools completion path: a stream and
// buffered fallback that yield nothing must fail loudly, like the
// tool path's empty-round guard, instead of ending as a silent READY.

package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/Ady-JK/paladin/core/internal/inference"
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

func TestRunPlainRetriesOnlyBeforeStreaming(t *testing.T) {
	old := retryUnit
	retryUnit = time.Millisecond
	t.Cleanup(func() { retryUnit = old })
	prov := &mockProvider{streams: []streamScript{
		{err: inference.RequestError("stream: status 503")},
		{chunks: []inference.Chunk{inference.TextChunk("recovered")}},
	}}
	a, _, done := newAgentForTest(t, prov, testSession(t, "plain-retry"), "hi", false, false)
	defer close(done)
	out, err := a.Run()
	if err != nil || out.Reply != "recovered" || prov.calls != 2 {
		t.Fatalf("outcome=%+v calls=%d error=%v", out, prov.calls, err)
	}
}

func TestRunPlainDoesNotRetryAfterText(t *testing.T) {
	prov := &mockProvider{streams: []streamScript{
		{chunks: []inference.Chunk{inference.TextChunk("partial")}, err: inference.RequestError("stream: status 503")},
		{chunks: []inference.Chunk{inference.TextChunk("duplicate")}},
	}}
	a, _, done := newAgentForTest(t, prov, testSession(t, "plain-partial"), "hi", false, false)
	defer close(done)
	_, err := a.Run()
	if err == nil || prov.calls != 1 {
		t.Fatalf("calls=%d error=%v, want one call and an error", prov.calls, err)
	}
}
