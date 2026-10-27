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

func anthropicProvider(t *testing.T, srv *httptest.Server) *OpenAIProvider {
	t.Helper()
	p := NewOpenAIProvider("oc", "claude-haiku-5-5", srv.URL, "k")
	p.isOpencode = true
	p.protocol = protocolAnthropic
	return p
}

func TestCompleteAnthropic(t *testing.T) {
	var gotPath, gotVersion, gotAuth string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotVersion = r.Header.Get("anthropic-version")
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&body)
		fmt.Fprint(w, `{"content":[{"type":"text","text":"hi"},{"type":"tool_use","id":"toolu_1","name":"read","input":{"path":"x"}}]}`)
	}))
	defer srv.Close()
	p := anthropicProvider(t, srv)
	resp, err := p.CompleteTools(context.Background(), "sys", []LlmMessage{{Role: "user", Content: "hi"}}, []ToolDefinition{{Name: "read", Parameters: map[string]any{"type": "object"}}}, "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/anthropic/v1/messages" || gotVersion != anthropicVersion {
		t.Fatalf("path=%q version=%q", gotPath, gotVersion)
	}
	if gotAuth != "Bearer k" {
		t.Fatalf("auth %q", gotAuth)
	}
	if body["system"] != "sys" || body["model"] != "claude-haiku-5-5" {
		t.Fatalf("body %#v", body)
	}
	if resp.Content != "hi" || len(resp.ToolCalls) != 1 || resp.ToolCalls[0].ID != "toolu_1" || resp.ToolCalls[0].Arguments != `{"path":"x"}` {
		t.Fatalf("resp %+v", resp)
	}
}

func TestStreamAnthropic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\n")
		fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_1\",\"name\":\"read\"}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"path\\\":\\\"x\\\"}\"}}\n\n")
		fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer srv.Close()
	p := anthropicProvider(t, srv)
	tx := make(chan Chunk, 32)
	done := make(chan struct{})
	if err := p.StreamTools(context.Background(), "sys", []LlmMessage{{Role: "user", Content: "hi"}}, nil, "sess-1", tx, done); err != nil {
		t.Fatal(err)
	}
	close(done)
	close(tx)
	var text strings.Builder
	notices := 0
	var calls []ToolCall
	for c := range tx {
		text.WriteString(c.Text)
		if c.ToolNotice != nil {
			notices++
		}
		if len(c.ToolCalls) > 0 {
			calls = c.ToolCalls
		}
	}
	if text.String() != "hi" || notices != 1 {
		t.Fatalf("text=%q notices=%d", text.String(), notices)
	}
	if len(calls) != 1 || calls[0].ID != "toolu_1" || calls[0].Arguments != `{"path":"x"}` {
		t.Fatalf("calls %+v", calls)
	}
}

func TestAnthropicMessageShapes(t *testing.T) {
	id := "toolu_1"
	req := toAnthropicRequest("sys", []LlmMessage{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "doing", ToolCalls: []ToolCall{{ID: id, Name: "read", Arguments: `{"path":"x"}`}}},
		{Role: "tool", Content: "out", ToolCallID: &id, Name: strPtr("read")},
	}, nil)
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	for _, want := range []string{`"system":"sys"`, `"type":"tool_use"`, `"tool_use_id":"toolu_1"`, `"type":"tool_result"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s in %s", want, got)
		}
	}
}

func strPtr(s string) *string { return &s }
