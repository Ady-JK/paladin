package agent

import (
	"testing"

	"github.com/Ady-JK/paladin/core/internal/inference"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

func TestInterruptedToolAnnouncementsAndRefusalsSettleWholeBatch(t *testing.T) {
	for _, phase := range []string{"announce", "refuse"} {
		t.Run(phase, func(t *testing.T) {
			a, _, _, done := testAgent(t, &mockProvider{})
			// Close the consumer after the first event. An unbuffered channel
			// makes the next emission fail deterministically, mid-batch.
			events := make(chan ChatChunk)
			a.tx = events
			go func() {
				<-events
				close(done)
			}()
			calls := []inference.ToolCall{
				{ID: "a", Name: "read", Arguments: "{}"},
				{ID: "b", Name: "read", Arguments: "{}"},
				{ID: "c", Name: "read", Arguments: "{}"},
			}
			a.appendAssistant("", calls)
			var err error
			if phase == "announce" {
				err = a.announceToolCalls(calls)
			} else {
				err = a.refuseRereadCalls(calls)
			}
			if err == nil {
				t.Fatal("expected interruption")
			}
			// Check live messages and raw records, without history repair
			// hiding the missing results this test is meant to prevent.
			assertPaired(t, a.messages)
			results := map[string]int{}
			for _, row := range a.records {
				if row.Role == transcript.RoleTool && row.ToolCallID != nil {
					results[*row.ToolCallID]++
					if !row.IsError {
						t.Fatal("unexecuted tool must not be recorded as successful")
					}
				}
			}
			for _, call := range calls {
				if results[call.ID] != 1 {
					t.Fatalf("call %s has %d results, want exactly one", call.ID, results[call.ID])
				}
			}
		})
	}
}
