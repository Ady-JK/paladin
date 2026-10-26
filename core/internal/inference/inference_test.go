package inference

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/config"
)

func providerFor(url string) *OpenAIProvider {
	return NewOpenAIProvider("test", "m", url, "k")
}

func TestCompleteToolsParsesToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices": [{"message": {"content": "hi", "tool_calls": [{"id": "c1", "function": {"name": "read", "arguments": "{\"path\":\"x\"}"}}]}}]}`)
	}))
	defer server.Close()
	p := providerFor(server.URL)
	resp, err := p.CompleteTools(context.Background(), "sys", nil, nil, "sess")
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "hi" || len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "read" {
		t.Fatalf("got %+v", resp)
	}
}

func TestStreamAssemblesFragmentedToolCalls(t *testing.T) {
	sse := "data: {\"choices\":[{\"delta\":{\"content\":\"Hel\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"c1\",\"function\":{\"name\":\"re\",\"arguments\":\"{\\\"pa\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"th\\\"}\"}}]}}]}\n\n" +
		"data: [DONE]\n\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sse)
	}))
	defer server.Close()
	p := providerFor(server.URL)
	tx := make(chan Chunk, 64)
	done := make(chan struct{})
	defer close(done)
	if err := p.StreamTools(context.Background(), "sys", nil, nil, "sess", tx, done); err != nil {
		t.Fatal(err)
	}
	close(tx)
	var texts strings.Builder
	notices := 0
	var finalCalls []ToolCall
	for chunk := range tx {
		texts.WriteString(chunk.Text)
		if chunk.ToolNotice != nil {
			notices++
		}
		if len(chunk.ToolCalls) > 0 {
			finalCalls = chunk.ToolCalls
		}
	}
	_ = config.PlaceholderKey
	if texts.String() != "Hel" {
		t.Fatalf("got %q", texts.String())
	}
	if notices != 1 {
		t.Fatalf("notices %d", notices)
	}
	if len(finalCalls) != 1 || finalCalls[0].Arguments != "{\"path\"}" {
		t.Fatalf("got %+v", finalCalls)
	}
}

func TestMapsHTTPErrorsWithoutLeakingKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		fmt.Fprint(w, "bad key")
	}))
	defer server.Close()
	p := NewOpenAIProvider("test", "m", server.URL, "sk-secret-xyz")
	err := func() error {
		_, err := p.CompleteTools(context.Background(), "sys", nil, nil, "sess")
		return err
	}()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("got %q", err)
	}
	if strings.Contains(err.Error(), "sk-secret-xyz") {
		t.Fatalf("key leaked: %q", err)
	}
}

func TestListsModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data": [{"id": "a"}, {"id": "b"}]}`)
	}))
	defer server.Close()
	p := providerFor(server.URL)
	models, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0] != "a" || models[1] != "b" {
		t.Fatalf("got %v", models)
	}
}

func TestChunkTextCarriesText(t *testing.T) {
	if got := TextChunk("hi"); got.Text != "hi" || len(got.ToolCalls) != 0 {
		t.Fatalf("got %+v", got)
	}
}
