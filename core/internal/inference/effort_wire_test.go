package inference

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// capture serves one canned response and keeps the last request body.
func capture(t *testing.T, contentType, reply string) (*httptest.Server, *map[string]any) {
	t.Helper()
	body := map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = map[string]any{}
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", contentType)
		fmt.Fprint(w, reply)
	}))
	t.Cleanup(srv.Close)
	return srv, &body
}

const chatReply = `{"choices":[{"message":{"content":"hi","reasoning_content":"pondering"}}]}`

func TestChatSendsTheChosenEffort(t *testing.T) {
	srv, body := capture(t, "application/json", chatReply)
	base := NewOpenAIProvider("lite", "claude-sonnet-5", srv.URL, "k")
	p, err := WithEffort(base, "xhigh")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.CompleteTools(context.Background(), "", []LlmMessage{{Role: "user", Content: "hi"}}, nil, "s"); err != nil {
		t.Fatal(err)
	}
	if (*body)["reasoning_effort"] != "xhigh" {
		t.Fatalf("body %v", *body)
	}
	if _, err := base.CompleteTools(context.Background(), "", []LlmMessage{{Role: "user", Content: "hi"}}, nil, "s"); err != nil {
		t.Fatal(err)
	}
	if _, set := (*body)["reasoning_effort"]; set {
		t.Fatal("the base provider must keep the provider default")
	}
}

func TestWithEffortRejectsLevelsTheModelLacks(t *testing.T) {
	base := NewOpenAIProvider("lite", "claude-haiku-4-5-20251001", "http://x/v1", "k")
	if _, err := WithEffort(base, "max"); err == nil {
		t.Fatal("haiku 4.5 has no max")
	}
	if p, err := WithEffort(base, ""); err != nil || p != Provider(base) {
		t.Fatal("empty effort is the provider itself")
	}
	if _, err := WithEffort(NewOpenAIProvider("lite", "gpt-4o-mini", "http://x/v1", "k"), "low"); err == nil {
		t.Fatal("no thinking on gpt-4o-mini")
	}
}

func TestResponsesSendsTheEffortWithASummary(t *testing.T) {
	srv, body := capture(t, "application/json", `{"id":"r","object":"response","status":"completed","model":"gpt-5","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`)
	base := responsesProvider(t, srv)
	base.model = "gpt-5"
	p, err := WithEffort(base, "minimal")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.CompleteTools(context.Background(), "", []LlmMessage{{Role: "user", Content: "hi"}}, nil, "s"); err != nil {
		t.Fatal(err)
	}
	r, _ := (*body)["reasoning"].(map[string]any)
	if r["effort"] != "minimal" || r["summary"] != "auto" {
		t.Fatalf("body %v", *body)
	}
}

func TestChatCompleteReturnsReasoning(t *testing.T) {
	srv, _ := capture(t, "application/json", chatReply)
	resp, err := NewOpenAIProvider("lite", "claude-sonnet-5", srv.URL, "k").CompleteTools(context.Background(), "", []LlmMessage{{Role: "user", Content: "hi"}}, nil, "s")
	if err != nil || resp.Content != "hi" || resp.Reasoning != "pondering" {
		t.Fatalf("%+v %v", resp, err)
	}
}

func TestChatStreamSeparatesReasoningFromText(t *testing.T) {
	sse := "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"Let me \"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"reasoning\":\"think.\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"Answer\"}}]}\n\n" +
		"data: [DONE]\n\n"
	srv, _ := capture(t, "text/event-stream", sse)
	text, reasoning := drain(t, NewOpenAIProvider("lite", "claude-sonnet-5", srv.URL, "k"))
	if text != "Answer" || reasoning != "Let me think." {
		t.Fatalf("text %q reasoning %q", text, reasoning)
	}
}

func drain(t *testing.T, p Provider) (string, string) {
	t.Helper()
	tx := make(chan Chunk, 64)
	done := make(chan struct{})
	defer close(done)
	if err := p.StreamTools(context.Background(), "", []LlmMessage{{Role: "user", Content: "hi"}}, nil, "s", tx, done); err != nil {
		t.Fatal(err)
	}
	close(tx)
	var text, reasoning strings.Builder
	for c := range tx {
		text.WriteString(c.Text)
		reasoning.WriteString(c.Reasoning)
	}
	return text.String(), reasoning.String()
}

func TestARejectedEffortIsDroppedAndReported(t *testing.T) {
	calls, withEffort := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		raw, _ := io.ReadAll(r.Body)
		if strings.Contains(string(raw), "reasoning_effort") {
			withEffort++
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":{"message":"reasoning_effort is not supported for this model","type":"invalid_request_error"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()
	p, err := WithEffort(NewOpenAIProvider("lite", "claude-sonnet-5", srv.URL, "k"), "high")
	if err != nil {
		t.Fatal(err)
	}
	for round := 0; round < 2; round++ {
		tx := make(chan Chunk, 16)
		done := make(chan struct{})
		if err := p.StreamTools(context.Background(), "", []LlmMessage{{Role: "user", Content: "hi"}}, nil, "s", tx, done); err != nil {
			t.Fatal(err)
		}
		close(done)
		close(tx)
		var text, dropped string
		for c := range tx {
			text += c.Text
			if c.EffortDropped != "" {
				dropped = c.EffortDropped
			}
		}
		if text != "ok" {
			t.Fatalf("round %d text %q", round, text)
		}
		if (round == 0) != (dropped == "high") {
			t.Fatalf("round %d dropped %q", round, dropped)
		}
	}
	if withEffort != 1 || calls != 3 {
		t.Fatalf("calls %d with effort %d", calls, withEffort)
	}
}

func TestOtherBadRequestsKeepTheEffort(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":{"message":"prompt is too long"}}`)
	}))
	defer srv.Close()
	p, _ := WithEffort(NewOpenAIProvider("lite", "claude-sonnet-5", srv.URL, "k"), "high")
	if _, err := p.CompleteTools(context.Background(), "", []LlmMessage{{Role: "user", Content: "hi"}}, nil, "s"); err == nil {
		t.Fatal("want the error")
	}
	if calls != 1 {
		t.Fatalf("calls %d", calls)
	}
}
