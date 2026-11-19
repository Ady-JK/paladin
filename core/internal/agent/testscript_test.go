// testscript.go holds the stream-script helpers the agent tests build
// scripted provider rounds from. They used to live in doom_test.go; the
// guards it tested are gone, but every other scripted test still needs
// these.

package agent

import (
	"testing"

	"github.com/Ady-JK/paladin/core/internal/inference"
)

func textRound(s string) streamScript {
	return streamScript{chunks: []inference.Chunk{inference.TextChunk(s)}}
}

func callRound(id, name, args string) streamScript {
	return streamScript{chunks: []inference.Chunk{{ToolCalls: []inference.ToolCall{{ID: id, Name: name, Arguments: args}}}}}
}

func toolResultsByID(t *testing.T, a *Agent) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, m := range a.messages {
		if m.Role == "tool" && m.ToolCallID != nil {
			out[*m.ToolCallID] = m.Content
		}
	}
	return out
}
