package agent

import (
	"fmt"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/transcript"
)

// BenchmarkLlmMessagesLargeToolBatch measures transcript repair for a large
// assistant tool-call batch, including matching each stored result to its call.
func BenchmarkLlmMessagesLargeToolBatch(b *testing.B) {
	const count = 1000
	calls := make([]transcript.ToolCall, count)
	history := make([]transcript.Message, 1, count+1)
	for i := range count {
		id := fmt.Sprintf("call-%04d", i)
		calls[i] = transcript.ToolCall{ID: id, Name: "read", Arguments: "{}"}
		idCopy := id
		history = append(history, transcript.Message{Role: transcript.RoleTool, ToolCallID: &idCopy, Content: "ok"})
	}
	history[0] = transcript.Message{Role: transcript.RoleAssistant, ToolCalls: &calls}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if got := LlmMessages(history, true); len(got) != count+1 {
			b.Fatalf("got %d messages", len(got))
		}
	}
}
