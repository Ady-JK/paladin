package inference

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func drainUsage(t *testing.T, p *OpenAIProvider, chunks chan Chunk) (text string, calls []ToolCall, usage *Usage) {
	t.Helper()
	var sb strings.Builder
	for c := range chunks {
		sb.WriteString(c.Text)
		if len(c.ToolCalls) > 0 {
			calls = c.ToolCalls
		}
		if c.Usage != nil {
			usage = c.Usage
		}
	}
	return sb.String(), calls, usage
}

func TestChatUnaryCapturesOpencodeUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"hi"}}],"usage":{"prompt_tokens":53,"completion_tokens":8,"total_tokens":61,"completion_tokens_details":{"reasoning_tokens":7},"cost":0,"opencode":{"billing_source":"go","list_cost":0.000772,"tokens":{"input":53,"output":8,"reasoning":7,"cache_read":10,"cache_write_5m":2,"cache_write_1h":3}}}}`)
	}))
	defer srv.Close()
	p := NewOpenAIProvider("oc", "beam-free", srv.URL, "k")
	p.isOpencode = true
	p.protocol = protocolChat
	resp, err := p.CompleteTools(context.Background(), "", []LlmMessage{{Role: "user", Content: "hi"}}, nil, "sess")
	if err != nil {
		t.Fatal(err)
	}
	u := resp.Usage
	if u == nil {
		t.Fatal("no usage")
	}
	if u.PromptTokens != 53 || u.CompletionTokens != 8 || u.TotalTokens != 61 || u.ReasoningTokens != 7 {
		t.Fatalf("tokens %+v", u)
	}
	if !u.HasCost || u.Cost != 0 || u.ListCost != 0.000772 {
		t.Fatalf("cost %+v", u)
	}
	if u.CacheReadTokens != 10 || u.CacheWriteTokens != 5 {
		t.Fatalf("cache %+v", u)
	}
}

func TestChatStreamCapturesUsageAndAsksForIt(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":20,\"completion_tokens\":4,\"total_tokens\":24,\"cost\":0.00014,\"prompt_tokens_details\":{\"cached_tokens\":6}}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()
	p := NewOpenAIProvider("oc", "beam-free", srv.URL, "k")
	p.isOpencode = true
	p.protocol = protocolChat
	tx := make(chan Chunk, 32)
	done := make(chan struct{})
	if err := p.StreamTools(context.Background(), "", []LlmMessage{{Role: "user", Content: "hi"}}, nil, "sess", tx, done); err != nil {
		t.Fatal(err)
	}
	close(done)
	close(tx)
	text, _, usage := drainUsage(t, p, tx)
	if text != "hi" {
		t.Fatalf("text %q", text)
	}
	if !strings.Contains(gotBody, `"include_usage":true`) {
		t.Fatalf("stream_options missing: %s", gotBody)
	}
	if usage == nil {
		t.Fatal("no usage chunk")
	}
	if usage.TotalTokens != 24 || usage.CacheReadTokens != 6 {
		t.Fatalf("got %+v", usage)
	}
	if !usage.HasCost || usage.Cost != 0.00014 {
		t.Fatalf("cost %+v", usage)
	}
}

func TestChatUnaryPlainOmitsCost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"hi"}}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`)
	}))
	defer srv.Close()
	p := providerFor(srv.URL)
	resp, err := p.CompleteTools(context.Background(), "", nil, nil, "sess")
	if err != nil {
		t.Fatal(err)
	}
	u := resp.Usage
	if u == nil || u.HasCost || u.TotalTokens != 7 {
		t.Fatalf("got %+v", u)
	}
}

func TestResponsesUnaryCapturesUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"resp_1","object":"response","status":"completed","model":"m","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi"}]}],"usage":{"input_tokens":30,"output_tokens":6,"total_tokens":36,"output_tokens_details":{"reasoning_tokens":2},"cost":0.001,"opencode":{"list_cost":0.002}}}`)
	}))
	defer srv.Close()
	p := responsesProvider(t, srv)
	resp, err := p.CompleteTools(context.Background(), "", []LlmMessage{{Role: "user", Content: "hi"}}, nil, "sess")
	if err != nil {
		t.Fatal(err)
	}
	u := resp.Usage
	if u == nil {
		t.Fatal("no usage")
	}
	if u.PromptTokens != 30 || u.CompletionTokens != 6 || u.ReasoningTokens != 2 || u.TotalTokens != 36 {
		t.Fatalf("tokens %+v", u)
	}
	if !u.HasCost || u.Cost != 0.001 || u.ListCost != 0.002 {
		t.Fatalf("cost %+v", u)
	}
}

func TestResponsesStreamCapturesUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n")
		fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"status\":\"completed\",\"model\":\"m\",\"output\":[],\"usage\":{\"input_tokens\":30,\"output_tokens\":6,\"total_tokens\":36}}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()
	p := responsesProvider(t, srv)
	tx := make(chan Chunk, 32)
	done := make(chan struct{})
	if err := p.StreamTools(context.Background(), "", []LlmMessage{{Role: "user", Content: "hi"}}, nil, "sess", tx, done); err != nil {
		t.Fatal(err)
	}
	close(done)
	close(tx)
	_, _, usage := drainUsage(t, p, tx)
	if usage == nil || usage.TotalTokens != 36 || usage.PromptTokens != 30 {
		t.Fatalf("got %+v", usage)
	}
}

func TestAnthropicUnaryCapturesUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"content":[{"type":"text","text":"hi"}],"usage":{"input_tokens":40,"cache_creation_input_tokens":5,"cache_read_input_tokens":12,"output_tokens":9}}`)
	}))
	defer srv.Close()
	p := anthropicProvider(t, srv)
	resp, err := p.CompleteTools(context.Background(), "", []LlmMessage{{Role: "user", Content: "hi"}}, nil, "sess")
	if err != nil {
		t.Fatal(err)
	}
	u := resp.Usage
	if u == nil {
		t.Fatal("no usage")
	}
	if u.PromptTokens != 40 || u.CompletionTokens != 9 || u.TotalTokens != 49 {
		t.Fatalf("tokens %+v", u)
	}
	if u.CacheReadTokens != 12 || u.CacheWriteTokens != 5 {
		t.Fatalf("cache %+v", u)
	}
	if u.HasCost {
		t.Fatalf("anthropic should not report cost: %+v", u)
	}
}

func TestAnthropicStreamCapturesUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"usage\":{\"input_tokens\":40,\"cache_read_input_tokens\":12}}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\n")
		fmt.Fprint(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{},\"usage\":{\"output_tokens\":9}}\n\n")
		fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer srv.Close()
	p := anthropicProvider(t, srv)
	tx := make(chan Chunk, 32)
	done := make(chan struct{})
	if err := p.StreamTools(context.Background(), "", []LlmMessage{{Role: "user", Content: "hi"}}, nil, "sess", tx, done); err != nil {
		t.Fatal(err)
	}
	close(done)
	close(tx)
	text, _, usage := drainUsage(t, p, tx)
	if text != "hi" {
		t.Fatalf("text %q", text)
	}
	if usage == nil || usage.PromptTokens != 40 || usage.CompletionTokens != 9 || usage.CacheReadTokens != 12 {
		t.Fatalf("got %+v", usage)
	}
}

func TestGoogleUnaryCapturesUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"candidates": [{"content": {"role": "model", "parts": [{"text": "hi"}]}}], "usageMetadata": {"promptTokenCount": 50, "candidatesTokenCount": 7, "thoughtsTokenCount": 3, "cachedContentTokenCount": 11, "totalTokenCount": 57, "cost": 0.0004, "opencode": {"list_cost": 0.0009}}}`)
	}))
	defer srv.Close()
	p := googleProvider(t, srv)
	resp, err := p.CompleteTools(context.Background(), "", []LlmMessage{{Role: "user", Content: "hi"}}, nil, "sess")
	if err != nil {
		t.Fatal(err)
	}
	u := resp.Usage
	if u == nil {
		t.Fatal("no usage")
	}
	if u.PromptTokens != 50 || u.CompletionTokens != 7 || u.ReasoningTokens != 3 || u.CacheReadTokens != 11 || u.TotalTokens != 57 {
		t.Fatalf("tokens %+v", u)
	}
	if !u.HasCost || u.Cost != 0.0004 || u.ListCost != 0.0009 {
		t.Fatalf("cost %+v", u)
	}
}

func TestGoogleStreamKeepsLastUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"candidates\": [{\"content\": {\"parts\": [{\"text\": \"hi\"}]}}], \"usageMetadata\": {\"promptTokenCount\": 50, \"candidatesTokenCount\": 2, \"totalTokenCount\": 52}}\n\n")
		fmt.Fprint(w, "data: {\"candidates\": [{\"content\": {\"parts\": [{\"text\": \"!\"}]}}], \"usageMetadata\": {\"promptTokenCount\": 50, \"candidatesTokenCount\": 7, \"totalTokenCount\": 57}}\n\n")
	}))
	defer srv.Close()
	p := googleProvider(t, srv)
	tx := make(chan Chunk, 32)
	done := make(chan struct{})
	if err := p.StreamTools(context.Background(), "", []LlmMessage{{Role: "user", Content: "hi"}}, nil, "sess", tx, done); err != nil {
		t.Fatal(err)
	}
	close(done)
	close(tx)
	_, _, usage := drainUsage(t, p, tx)
	if usage == nil || usage.TotalTokens != 57 || usage.CompletionTokens != 7 {
		t.Fatalf("got %+v (want last-wins, not summed)", usage)
	}
}
