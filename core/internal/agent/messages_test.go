package agent

import (
	"encoding/json"
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

func TestModelMessagesIncludesCurrent(t *testing.T) {
	h := []transcript.Message{transcript.UserMessage("old")}
	with := LlmMessages(h, true)
	if len(with) != 1 || with[0].Content != "old" {
		t.Fatalf("got %+v", with)
	}
	a, _, _, done := testAgent(t, &mockProvider{}, 5)
	defer close(done)
	msgs := a.modelMessages("new", false)
	if len(msgs) != 1 || msgs[0].Content != "new" {
		t.Fatalf("got %+v", msgs)
	}
	resumed := a.modelMessages("new", true)
	if len(resumed) != 0 {
		t.Fatalf("resume should not append: %+v", resumed)
	}
}

func TestLlmMessagesEdgeCases(t *testing.T) {
	now := time.Now().UTC()
	// Assistant blank without calls is dropped.
	msgs := LlmMessages([]transcript.Message{
		{Role: transcript.RoleAssistant, Content: "  ", CreatedAt: now},
	}, true)
	if len(msgs) != 0 {
		t.Fatalf("got %+v", msgs)
	}
	// Assistant blank WITH calls survives (tool path).
	stored := []transcript.ToolCall{{ID: "c1", Name: "read", Arguments: "{}"}}
	msgs = LlmMessages([]transcript.Message{
		{Role: transcript.RoleAssistant, Content: "", CreatedAt: now, ToolCalls: &stored},
	}, true)
	if len(msgs) != 1 || len(msgs[0].ToolCalls) != 1 {
		t.Fatalf("got %+v", msgs)
	}
	// Empty tool-call IDs are skipped.
	stored2 := []transcript.ToolCall{{ID: "", Name: "read", Arguments: "{}"}}
	msgs = LlmMessages([]transcript.Message{
		{Role: transcript.RoleAssistant, Content: "x", CreatedAt: now, ToolCalls: &stored2},
	}, true)
	if len(msgs) != 1 || len(msgs[0].ToolCalls) != 0 {
		t.Fatalf("got %+v", msgs)
	}
	// Notify tool rows demote to user context.
	nid, nname := "c1#notify", "shell"
	msgs = LlmMessages([]transcript.Message{
		{Role: transcript.RoleAssistant, CreatedAt: now, ToolCalls: &[]transcript.ToolCall{{ID: "c1", Name: "shell", Arguments: "{}"}}},
		{Role: transcript.RoleTool, Content: "bg done", CreatedAt: now, ToolCallID: &nid, Name: &nname},
	}, true)
	if len(msgs) != 2 || msgs[1].Role != "user" {
		t.Fatalf("got %+v", msgs)
	}
	// System rows are not forwarded to the model.
	msgs = LlmMessages([]transcript.Message{
		{Role: transcript.RoleSystem, Content: "sys", CreatedAt: now},
	}, true)
	if len(msgs) != 0 {
		t.Fatalf("got %+v", msgs)
	}
	// Nil history -> empty non-nil slice.
	if out := LlmMessages(nil, true); out == nil || len(out) != 0 {
		t.Fatalf("got %+v", out)
	}
}

func TestNotifyContentShape(t *testing.T) {
	n := noticeForTest()
	content := notifyContent(&n)
	var wire map[string]any
	if err := json.Unmarshal([]byte(content), &wire); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"notice", "job_id", "status", "exit_code", "output", "truncated"} {
		if _, ok := wire[k]; !ok {
			t.Fatalf("missing %q in %s", k, content)
		}
	}
	if wire["notice"] != "background job finished" {
		t.Fatalf("got %+v", wire)
	}
}
