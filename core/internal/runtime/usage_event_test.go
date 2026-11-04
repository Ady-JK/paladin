// usage_event_test.go covers the wire event behind per-call usage: the
// hub publishes chat/usage whenever a call writes the ledger.

package runtime

import (
	"context"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/agent"
)

func TestHubPublishesUsageChanges(t *testing.T) {
	run := func(ctx context.Context, req agent.Request, gate func(func() error), tx chan<- agent.ChatChunk, _ <-chan struct{}) (agent.ChatOutcome, error) {
		tx <- agent.ChatChunk{SessionID: req.SessionID, UsageChanged: true}
		gate(func() error { return nil })
		return agent.ChatOutcome{SessionID: req.SessionID}, nil
	}
	h, log := steerHub(t, run)
	if _, _, err := h.Submit(agent.Request{SessionID: "s1", Message: "go"}, SubmitOpts{}); err != nil {
		t.Fatal(err)
	}
	log.waitFor(t, "chat/usage", func(evs []TurnEvent) bool {
		for _, e := range evs {
			if e.Event == "chat/usage" {
				return true
			}
		}
		return false
	})
}
