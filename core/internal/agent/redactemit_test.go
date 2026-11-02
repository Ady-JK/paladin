// redactemit_test.go pins secret masking at the user-visible error
// emission points: the turn's error row, the retry notice, and the
// failed-compaction divider. Gateways may echo request metadata into
// error bodies; none of it may reach the event stream verbatim.

package agent

import (
	"errors"
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/transcript"
)

const emitSecret = "sk-test-Key-1234567890abcdef"

func TestRetryReasonMasksSecrets(t *testing.T) {
	got := retryReason(errors.New("status 500: upstream leaked " + emitSecret + " in body"))
	if strings.Contains(got, emitSecret) {
		t.Fatalf("secret leaked: %q", got)
	}
	if !strings.Contains(got, "status 500") {
		t.Fatalf("non-secret context lost: %q", got)
	}
}

func TestFailedCompactionMasksSecrets(t *testing.T) {
	provider := &mockProvider{completeErr: errors.New("summarizer 400: key " + emitSecret + " rejected")}
	sess := testSession(t, "00000000-0000-4000-8000-000000000036")
	for i := 0; i < 6; i++ {
		_ = sess.Append(transcript.RoleUser, strings.Repeat("question ", 50))
		_ = sess.Append(transcript.RoleAssistant, strings.Repeat("answer ", 50))
	}
	d, _, providers, _ := testDeps(t, sess)
	providers.provider = provider
	// Own the chunk channel so the failed-compaction emission can be
	// inspected after the run.
	tx := make(chan ChatChunk, 64)
	d.Tx = tx
	a, err := DefaultTemplate().Build(d, Request{SessionID: sess.Meta.SessionID, Compact: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Run(); err == nil {
		t.Fatal("want compaction failure")
	}
	var found bool
	for _, c := range drainChunks(tx) {
		if c.Compaction == nil || c.Compaction.Phase != "failed" {
			continue
		}
		found = true
		if strings.Contains(c.Compaction.Error, emitSecret) {
			t.Fatalf("secret leaked: %q", c.Compaction.Error)
		}
	}
	if !found {
		t.Fatal("no failed-compaction event")
	}
}
