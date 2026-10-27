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

func googleProvider(t *testing.T, srv *httptest.Server) *OpenAIProvider {
	t.Helper()
	p := NewOpenAIProvider("oc", "gemini-4-argon", srv.URL, "k")
	p.isOpencode = true
	p.protocol = protocolGoogle
	return p
}

func TestCompleteGoogle(t *testing.T) {
	var gotPath, gotAlt string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAlt = r.URL.Query().Get("alt")
		_ = json.NewDecoder(r.Body).Decode(&body)
		fmt.Fprint(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"hi"},{"functionCall":{"id":"call_1","name":"read","args":{"path":"x"}},"thoughtSignature":"sig-1"}]}}]}`)
	}))
	defer srv.Close()
	p := googleProvider(t, srv)
	resp, err := p.CompleteTools(context.Background(), "sys", []LlmMessage{{Role: "user", Content: "hi"}}, []ToolDefinition{{Name: "read", Parameters: map[string]any{"type": "object"}}}, "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/google/v1beta/models/gemini-4-argon:generateContent" || gotAlt != "" {
		t.Fatalf("path=%q alt=%q", gotPath, gotAlt)
	}
	if body["systemInstruction"] == nil || body["contents"] == nil {
		t.Fatalf("body %#v", body)
	}
	if resp.Content != "hi" || len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "read" || resp.ToolCalls[0].Arguments != `{"path":"x"}` {
		t.Fatalf("resp %+v", resp)
	}
	if resp.ToolCalls[0].ID != "call_1" || resp.ToolCalls[0].Signature != "sig-1" {
		t.Fatalf("call %+v", resp.ToolCalls[0])
	}
}

func TestStreamGoogle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("alt"); got != "sse" {
			t.Errorf("alt %q", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"hi\"}]}}]}\n\n")
		fmt.Fprint(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"functionCall\":{\"name\":\"read\",\"args\":{\"path\":\"x\"}}}]}}]}\n\n")
	}))
	defer srv.Close()
	p := googleProvider(t, srv)
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
	if len(calls) != 1 || calls[0].Name != "read" || calls[0].Arguments != `{"path":"x"}` || calls[0].ID == "" {
		t.Fatalf("calls %+v", calls)
	}
}

func TestGoogleMessageShapes(t *testing.T) {
	id := "gemini-1"
	req := toGoogleRequest("sys", []LlmMessage{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "doing", ToolCalls: []ToolCall{{ID: id, Name: "read", Arguments: `{"path":"x"}`, Signature: "sig-1"}}},
		{Role: "tool", Content: `{"content":"out"}`, ToolCallID: &id, Name: strPtr("read")},
	}, []ToolDefinition{{Name: "read", Parameters: map[string]any{"type": "object"}}})
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	for _, want := range []string{`"systemInstruction"`, `"role":"model"`, `"functionCall"`, `"functionResponse"`, `"functionDeclarations"`, `"thoughtSignature":"sig-1"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s in %s", want, got)
		}
	}
}
