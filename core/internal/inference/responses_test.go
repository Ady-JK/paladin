package inference

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/config"
)

func responsesProvider(t *testing.T, srv *httptest.Server) *OpenAIProvider {
	t.Helper()
	p := NewOpenAIProvider("oc", "muse-spark-1.3-contributor", srv.URL, "k")
	p.isOpencode = true
	p.protocol = protocolResponses
	return p
}

func TestCompleteResponsesParsesTextAndCalls(t *testing.T) {
	var gotPath, gotUA, gotSess string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotUA = r.Header.Get("User-Agent")
		gotSess = r.Header.Get(config.OpencodeSessionHeader)
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"id":"resp_1","object":"response","status":"completed","model":"muse-spark-1.3-contributor",
			"output":[
				{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]},
				{"type":"function_call","id":"item_1","call_id":"call_1","name":"read","arguments":"{\"path\":\"x\"}"}
			]}`)
	}))
	defer srv.Close()
	p := responsesProvider(t, srv)
	resp, err := p.CompleteTools(context.Background(), "sys", []LlmMessage{{Role: "user", Content: "hi"}}, []ToolDefinition{{Name: "read", Parameters: map[string]any{"type": "object"}}}, "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/responses" {
		t.Fatalf("path %q", gotPath)
	}
	if gotUA != config.OpencodeUserAgent || gotSess != "sess-1" {
		t.Fatalf("headers ua=%q sess=%q", gotUA, gotSess)
	}
	if body["model"] != "muse-spark-1.3-contributor" || body["instructions"] != "sys" {
		t.Fatalf("body %#v", body)
	}
	if body["prompt_cache_key"] != "sess-1" {
		t.Fatalf("cache key %#v", body["prompt_cache_key"])
	}
	if resp.Content != "hello" || len(resp.ToolCalls) != 1 || resp.ToolCalls[0].ID != "call_1" || resp.ToolCalls[0].Name != "read" {
		t.Fatalf("resp %+v", resp)
	}
}

func TestStreamResponsesAssemblesTextAndCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"id\":\"item_1\",\"type\":\"function_call\",\"call_id\":\"call_1\",\"name\":\"read\",\"arguments\":\"\"}}\n\n")
		fmt.Fprint(w, "data: {\"type\":\"response.function_call_arguments.delta\",\"item_id\":\"item_1\",\"output_index\":0,\"delta\":\"{\\\"path\\\":\\\"x\\\"}\"}\n\n")
		fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"output_index\":1,\"content_index\":0,\"delta\":\"hello\"}\n\n")
		fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"object\":\"response\",\"status\":\"completed\",\"model\":\"muse-spark-1.3-contributor\",\"output\":[")
		fmt.Fprint(w, "{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"hello\"}]},")
		fmt.Fprint(w, "{\"type\":\"function_call\",\"id\":\"item_1\",\"call_id\":\"call_1\",\"name\":\"read\",\"arguments\":\"{\\\"path\\\":\\\"x\\\"}\"}]}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()
	p := responsesProvider(t, srv)
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
	if text.String() != "hello" || notices != 1 {
		t.Fatalf("text=%q notices=%d", text.String(), notices)
	}
	if len(calls) != 1 || calls[0].ID != "call_1" || calls[0].Arguments != `{"path":"x"}` {
		t.Fatalf("calls %+v", calls)
	}
}

func TestStreamResponsesForwardsReasoningLive(t *testing.T) {
	// Reasoning models think before answering. The summary deltas
	// must stream (so the UI shows progress) without suppressing the
	// final answer carried by the completed envelope.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"response.reasoning_summary_text.delta\",\"output_index\":0,\"content_index\":0,\"delta\":\"thinking\"}\n\n")
		fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"object\":\"response\",\"status\":\"completed\",\"model\":\"muse-spark-1.3-contributor\",\"output\":[")
		fmt.Fprint(w, "{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"done\"}]}]}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()
	p := responsesProvider(t, srv)
	tx := make(chan Chunk, 32)
	done := make(chan struct{})
	if err := p.StreamTools(context.Background(), "sys", []LlmMessage{{Role: "user", Content: "hi"}}, nil, "sess-1", tx, done); err != nil {
		t.Fatal(err)
	}
	close(done)
	close(tx)
	var text strings.Builder
	for c := range tx {
		text.WriteString(c.Text)
	}
	if text.String() != "thinkingdone" {
		t.Fatalf("got %q", text.String())
	}
}

func TestResponsesInputShapes(t *testing.T) {
	id := "call_1"
	input := toResponsesInput([]LlmMessage{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "doing", ToolCalls: []ToolCall{{ID: id, Name: "read", Arguments: "{}"}}},
		{Role: "tool", Content: "out", ToolCallID: &id},
	})
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	for _, want := range []string{`"role":"user"`, `"role":"assistant"`, `"type":"function_call"`, `"call_id":"call_1"`, `"type":"function_call_output"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s in %s", want, got)
		}
	}
}
