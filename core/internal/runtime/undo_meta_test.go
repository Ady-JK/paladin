package runtime

import (
	"testing"

	"github.com/Ady-JK/paladin/core/internal/transcript"
)

// Clearing a revert must not write back the stale metadata the revert
// loaded earlier: a change made meanwhile (a rename, a new subagent) stays.
func TestClearRevertKeepsConcurrentMetaChanges(t *testing.T) {
	// Local mock model only; nothing leaves the machine.
	rt, ws, id, _ := agentRuntime(t, func(modelCall) modelReply { return modelReply{Text: "ok"} })
	if err := rt.transcripts.UpdateMeta(ws, id, func(m *transcript.Meta) error {
		m.Revert = &transcript.Revert{Index: 1}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	stale, err := rt.transcripts.Load(ws, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.transcripts.Rename(ws, id, "renamed meanwhile"); err != nil {
		t.Fatal(err)
	}
	if err := rt.clearRevert(&stale); err != nil {
		t.Fatal(err)
	}
	now, err := rt.transcripts.Load(ws, id)
	if err != nil {
		t.Fatal(err)
	}
	if now.Meta.Revert != nil {
		t.Fatalf("revert not cleared: %+v", now.Meta.Revert)
	}
	if now.Meta.Title != "renamed meanwhile" {
		t.Fatalf("a concurrent change was overwritten: title %q", now.Meta.Title)
	}
	if stale.Meta.Revert != nil {
		t.Fatal("the caller's copy still holds the revert")
	}
}
