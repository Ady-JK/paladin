package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/inference"
)

type panicProvider struct{ mockProvider }

func (p *panicProvider) StreamTools(context.Context, string, []inference.LlmMessage, []inference.ToolDefinition, string, chan<- inference.Chunk, <-chan struct{}) error {
	panic("provider bug")
}

func TestProviderPanicFailsTurnInsteadOfCrashing(t *testing.T) {
	for _, withTools := range []bool{true, false} {
		sess := testSession(t, "00000000-0000-4000-8000-000000000009")
		a, _, done := newAgentForTest(t, &panicProvider{}, sess, 5, "hi", false, withTools)
		_, err := a.Run()
		close(done)
		if err == nil || !strings.Contains(err.Error(), "provider bug") {
			t.Fatalf("withTools=%v: want a turn error naming the panic, got %v", withTools, err)
		}
	}
}
