package inference

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/config"
)

func TestCopilotProviderListsAndCompletes(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer test-access-token" || r.Header.Get("Copilot-Integration-Id") == "" {
			t.Errorf("missing Copilot authentication headers")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"claude-haiku"},{"id":"text-embedding-3-small"}]}`))
		case "/chat/completions":
			wantInitiator := "user"
			if calls == 3 {
				wantInitiator = "agent"
			}
			if r.Header.Get("X-Initiator") != wantInitiator {
				t.Errorf("wrong Copilot initiator")
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["model"] != "claude-haiku" {
				t.Errorf("unexpected request body")
			}
			_, _ = w.Write([]byte(`{"id":"1","object":"chat.completion","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"hello"}}]}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	p, err := BuildProvider(config.ApiTypeCopilot, "github-copilot", "claude-haiku", server.URL, "test-access-token")
	if err != nil {
		t.Fatal(err)
	}
	models, err := p.ListModels(context.Background())
	if err != nil || len(models) != 1 || models[0] != "claude-haiku" {
		t.Fatalf("models = %v, error = %v", models, err)
	}
	result, err := p.CompleteTools(context.Background(), "", []LlmMessage{{Role: "user", Content: "Say hello"}}, nil, "session")
	if err != nil || result.Content != "hello" {
		t.Fatalf("result = %+v, error = %v", result, err)
	}
	_, err = p.CompleteTools(context.Background(), "", []LlmMessage{{Role: "assistant", Content: "working"}}, nil, "session")
	if err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("got %d requests", calls)
	}
}
