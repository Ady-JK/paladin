package turn

import (
	"testing"
	"time"

	"github.com/Ady-JK/paladin/core/internal/transcript"
)

func TestToolResultsDemoteToUserWithoutTools(t *testing.T) {
	callID, name := "c1", "shell"
	history := []transcript.Message{
		transcript.UserMessage("hi"),
		{Role: transcript.RoleTool, Content: "out", CreatedAt: time.Now().UTC(), ToolCallID: &callID, Name: &name},
	}
	plain := LlmMessages(history, false)
	for _, m := range plain {
		if m.Role == "tool" {
			t.Fatal("tool role leaked without tools")
		}
	}
	with := LlmMessages(history, true)
	for _, m := range with {
		if m.Role == "tool" {
			t.Fatal("orphan tool result kept tool role")
		}
	}
}

func TestLinkedToolResultsKeepToolRole(t *testing.T) {
	stored := []transcript.ToolCall{{ID: "c1", Name: "read", Arguments: "{}"}}
	callID, name := "c1", "read"
	history := []transcript.Message{
		{Role: transcript.RoleAssistant, CreatedAt: time.Now().UTC(), ToolCalls: &stored},
		{Role: transcript.RoleTool, Content: "out", CreatedAt: time.Now().UTC(), ToolCallID: &callID, Name: &name},
	}
	with := LlmMessages(history, true)
	found := false
	for _, m := range with {
		if m.Role == "tool" {
			found = true
		}
	}
	if !found {
		t.Fatal("linked tool result lost tool role")
	}
}
