// hubredact_test.go pins secret masking on the turn's error row: a
// run failure carrying a secret-looking value must not reach TurnInfo
// (and the event stream) verbatim.

package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/agent"
)

func TestHubMasksSecretsInTurnError(t *testing.T) {
	h, runs, _ := newTestHub(t, 4)
	secret := "sk-test-Key-1234567890abcdef"
	runs.behavior["bad"] = func(context.Context, func(func() error), chan<- agent.ChatChunk) (agent.ChatOutcome, error) {
		return agent.ChatOutcome{}, errors.New("provider exploded: " + secret)
	}
	_, done := submit(t, h, "A", "bad")
	final := <-done
	if strings.Contains(final.Error, secret) {
		t.Fatalf("secret leaked: %q", final.Error)
	}
	if !strings.Contains(final.Error, "provider exploded") {
		t.Fatalf("non-secret context lost: %q", final.Error)
	}
}
