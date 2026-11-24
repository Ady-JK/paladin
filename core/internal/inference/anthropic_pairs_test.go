package inference

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Check the actual wire contract: every tool_use is answered exactly once in
// the immediately following user message, before any ordinary user content.
func checkAnthropicToolPairs(messages []anthropicMessage) error {
	for i, m := range messages {
		want := map[string]bool{}
		if i > 0 && messages[i-1].Role == "assistant" {
			for _, b := range messages[i-1].Content {
				if b.Type == "tool_use" {
					want[b.ID] = false
				}
			}
		}
		ordinary := false
		for _, b := range m.Content {
			if b.Type == "tool_use" {
				if i+1 == len(messages) || messages[i+1].Role != "user" {
					return fmt.Errorf("message %d: call %q has no following result message", i, b.ID)
				}
			}
			if b.Type != "tool_result" {
				ordinary = true
				continue
			}
			answered, known := want[b.ToolUseID]
			if m.Role != "user" || !known || answered || ordinary {
				return fmt.Errorf("message %d: misplaced/duplicate result %q", i, b.ToolUseID)
			}
			want[b.ToolUseID] = true
		}
		for id, answered := range want {
			if !answered {
				return fmt.Errorf("message %d: missing immediate result for %q", i, id)
			}
		}
	}
	return nil
}

func pairCall(ids ...string) LlmMessage {
	m := LlmMessage{Role: "assistant"}
	for _, id := range ids {
		m.ToolCalls = append(m.ToolCalls, ToolCall{ID: id, Name: "read", Arguments: "{}"})
	}
	return m
}

func pairResult(id, text string) LlmMessage {
	return LlmMessage{Role: "tool", ToolCallID: &id, Content: text}
}

func TestAnthropicToolPairing(t *testing.T) {
	copilot := &OpenAIProvider{isCopilot: true, name: "github-copilot", model: "claude-sonnet-5.5", meta: adaptiveMeta}
	builders := map[string]func([]LlmMessage) anthropicRequest{
		"copilot": func(ms []LlmMessage) anthropicRequest { return copilot.copilotMessagesRequest("", ms, nil) },
		"plain":   func(ms []LlmMessage) anthropicRequest { return toAnthropicRequest("", ms, nil) },
	}
	cases := []struct {
		name      string
		messages  []LlmMessage
		errors    int
		preserved []string
	}{
		{"valid parallel calls", []LlmMessage{pairCall("a", "b"), pairResult("a", "first"), pairResult("b", "second")}, 0, []string{"first", "second"}},
		{"user before results", []LlmMessage{pairCall("a", "b"), {Role: "user", Content: "steer"}, pairResult("a", "first"), pairResult("b", "second")}, 0, []string{"steer", "first", "second"}},
		{"reminder splits results", []LlmMessage{pairCall("a", "b"), pairResult("a", "first"), {Role: "user", Content: "job finished"}, pairResult("b", "second")}, 0, []string{"first", "second", "job finished"}},
		{"missing tail result", []LlmMessage{pairCall("a")}, 1, nil},
		{"missing sibling result", []LlmMessage{pairCall("a", "b"), pairResult("a", "first"), {Role: "assistant", Content: "next"}}, 1, []string{"first", "next"}},
		{"orphan result", []LlmMessage{pairResult("old", "orphan output"), {Role: "user", Content: "continue"}}, 0, []string{"orphan output"}},
		{"duplicate result", []LlmMessage{pairCall("a"), pairResult("a", "first"), pairResult("a", "duplicate output")}, 0, []string{"first", "duplicate output"}},
		{"unlinked result", []LlmMessage{pairCall("a"), {Role: "tool", Content: "unlinked output"}}, 1, []string{"unlinked output"}},
		{"late result after assistant", []LlmMessage{pairCall("a"), {Role: "assistant", Content: "intervening answer"}, pairResult("a", "late output")}, 1, []string{"intervening answer", "late output"}},
	}
	for builderName, build := range builders {
		for _, tc := range cases {
			t.Run(builderName+"/"+tc.name, func(t *testing.T) {
				input := append([]LlmMessage{{Role: "user", Content: "start"}}, tc.messages...)
				before, _ := json.Marshal(input)
				req := build(input)
				if err := checkAnthropicToolPairs(req.Messages); err != nil {
					t.Fatal(err)
				}
				errors := 0
				for _, m := range req.Messages {
					for _, b := range m.Content {
						if b.Type == "tool_result" && b.IsError {
							errors++
							if !strings.Contains(b.Content, "unknown") {
								t.Fatal("missing result must say the outcome is unknown, not invent success")
							}
						}
					}
				}
				if errors != tc.errors {
					t.Fatalf("error results=%d, want %d", errors, tc.errors)
				}
				raw, _ := json.Marshal(req.Messages)
				for _, text := range tc.preserved {
					if !strings.Contains(string(raw), text) {
						t.Fatalf("lost context %q", text)
					}
				}
				after, _ := json.Marshal(input)
				if string(before) != string(after) {
					t.Fatal("request building mutated its input history")
				}
			})
		}
	}
}

func TestCopilotToolPairsAreValidForStreamingAndBufferedRequests(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req anthropicRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := checkAnthropicToolPairs(req.Messages); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, simpleReply)
		} else {
			fmt.Fprint(w, `{"content":[{"type":"text","text":"ok"}]}`)
		}
	}))
	defer srv.Close()
	p := messagesProvider(t, srv, "claude-sonnet-5.5", adaptiveMeta)
	ms := []LlmMessage{{Role: "user", Content: "go"}, pairCall("a", "b"), pairResult("a", "one"), {Role: "user", Content: "reminder"}, pairResult("b", "two")}
	if _, err := p.CompleteTools(context.Background(), "", ms, nil, "s"); err != nil {
		t.Fatal(err)
	}
	if err := p.StreamTools(context.Background(), "", ms, nil, "s", make(chan Chunk, 64), make(chan struct{})); err != nil {
		t.Fatal(err)
	}
}

func TestCopilotToolPairsRepairLongInterruptedHistory(t *testing.T) {
	p := &OpenAIProvider{isCopilot: true, name: "github-copilot", model: "claude-sonnet-5.5", meta: adaptiveMeta}
	messages := []LlmMessage{{Role: "user", Content: "start"}}
	for i := 0; i < 251; i++ {
		id := fmt.Sprintf("call_%d", i)
		messages = append(messages, pairCall(id), pairResult(id, "recorded output"))
	}
	messages = append(messages, pairCall("unfinished"), LlmMessage{Role: "user", Content: "continue after interruption"})
	req := p.copilotMessagesRequest("", messages, nil)
	if len(req.Messages) < 503 {
		t.Fatal("regression must exercise a long history beyond message 502")
	}
	if err := checkAnthropicToolPairs(req.Messages); err != nil {
		t.Fatal(err)
	}
	last := req.Messages[len(req.Messages)-1].Content
	if len(last) != 2 || last[0].ToolUseID != "unfinished" || !last[0].IsError || last[1].Text != "continue after interruption" {
		t.Fatalf("interrupted tail = %+v", last)
	}
}

func TestAnthropicToolPairingKeepsThinkingAndImages(t *testing.T) {
	thinking := "signed thought"
	image := &anthropicImageSource{Type: "base64", MediaType: "image/png", Data: "test-image"}
	messages := []anthropicMessage{
		{Role: "assistant", Content: []anthropicBlock{
			{Type: "thinking", Thinking: &thinking, Signature: "native-signature"},
			{Type: "tool_use", ID: "a", Name: "read"}, {Type: "tool_use", ID: "b", Name: "read"},
		}},
		{Role: "user", Content: []anthropicBlock{{Type: "tool_result", ToolUseID: "a", Content: "first"}, {Type: "image", Source: image}}},
		{Role: "user", Content: []anthropicBlock{{Type: "text", Text: "reminder"}}},
		{Role: "user", Content: []anthropicBlock{{Type: "tool_result", ToolUseID: "b", Content: "second"}}},
	}
	got := pairAnthropicTools(messages)
	if err := checkAnthropicToolPairs(got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Content[0].Thinking != &thinking || got[0].Content[0].Signature != "native-signature" {
		t.Fatal("signed thinking was changed")
	}
	blocks := got[1].Content
	if len(blocks) != 4 || blocks[0].ToolUseID != "a" || blocks[1].ToolUseID != "b" || blocks[2].Source != image || blocks[3].Text != "reminder" {
		t.Fatalf("tool results, image or reminder were lost/reordered: %+v", blocks)
	}
}
