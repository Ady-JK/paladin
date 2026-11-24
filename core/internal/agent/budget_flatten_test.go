package agent

import (
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/inference"
)

// The wrap-up round has no tools, so the tool exchange before it is shown as
// text: a request that carries tool blocks without any tools is refused.
func TestFlattenToolMessagesLeavesNoToolBlocks(t *testing.T) {
	id, name := "c1", "shell"
	in := []inference.LlmMessage{
		{Role: "user", Content: "run it"},
		{Role: "assistant", Content: "on it", ToolCalls: []inference.ToolCall{{ID: id, Name: name, Arguments: `{"cmd":"ls"}`}}},
		{Role: "tool", Content: "file-a\nfile-b", ToolCallID: &id, Name: &name},
		{Role: "assistant", Content: "done"},
	}
	out := flattenToolMessages(in)
	if len(out) != 4 {
		t.Fatalf("got %d messages: %+v", len(out), out)
	}
	for _, m := range out {
		if len(m.ToolCalls) != 0 || m.Role == "tool" || m.ToolCallID != nil {
			t.Fatalf("a tool block survived: %+v", m)
		}
	}
	if !strings.Contains(out[1].Content, "on it") || !strings.Contains(out[1].Content, "shell") {
		t.Fatalf("the call is not described: %q", out[1].Content)
	}
	if out[2].Role != "user" || !strings.Contains(out[2].Content, "file-b") {
		t.Fatalf("the result is not kept: %+v", out[2])
	}
	if in[1].ToolCalls == nil || in[2].Role != "tool" {
		t.Fatal("the original messages were changed")
	}
}
