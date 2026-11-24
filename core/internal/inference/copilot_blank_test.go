package inference

import (
	"encoding/json"
	"testing"
)

// Copilot's Gemini answers HTTP 400 "invalid request body" to a user message
// whose content is empty. Other models accept one, so a history that began on
// Claude or GPT (an attachment-only prompt, an empty stray tool result) broke
// the first Gemini call after a model switch.
func TestChatBodyDropsBlankUserMessages(t *testing.T) {
	id, name := "c1", "read"
	msgs := []LlmMessage{
		{Role: "user", Content: "read a.txt"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "c1", Name: "read", Arguments: `{"path":"a.txt"}`}}},
		{Role: "tool", Content: "", ToolCallID: &id, Name: &name},
		{Role: "user", Content: ""},
		{Role: "user", Content: "  \n"},
		{Role: "assistant", Content: "done"},
		{Role: "user", Content: "thanks"},
	}
	raw, err := json.Marshal(toSDKMessages(msgs, nil))
	if err != nil {
		t.Fatal(err)
	}
	var got []struct {
		Role    string `json:"role"`
		Content any    `json:"content"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	var roles []string
	for _, m := range got {
		roles = append(roles, m.Role)
		if m.Role == "user" && m.Content == "" {
			t.Fatalf("blank user message reached the request: %s", raw)
		}
	}
	want := []string{"user", "assistant", "tool", "assistant", "user"}
	if len(roles) != len(want) {
		t.Fatalf("roles %v, want %v", roles, want)
	}
	for i := range want {
		if roles[i] != want[i] {
			t.Fatalf("roles %v, want %v", roles, want)
		}
	}
}

// A tool result may be empty: it still answers its call.
func TestChatBodyKeepsEmptyToolResults(t *testing.T) {
	id := "c1"
	msgs := []LlmMessage{
		{Role: "user", Content: "go"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "c1", Name: "read", Arguments: `{}`}}},
		{Role: "tool", Content: "", ToolCallID: &id},
	}
	out := toSDKMessages(msgs, nil)
	if len(out) != 3 || chatRole(out[2]) != "tool" {
		t.Fatalf("empty tool result was dropped: %d messages", len(out))
	}
}
