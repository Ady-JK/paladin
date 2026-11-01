package inference

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProviderDoesNotForwardAPIKeyAcrossOrigins(t *testing.T) {
	var leaked string
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	provider := NewOpenAIProvider("test", "model", source.URL, "private-key")
	_, _ = provider.CompleteTools(context.Background(), "system", nil, nil, "session")
	if leaked != "" {
		t.Fatal("provider API key was forwarded to a different origin")
	}
}

func TestProviderKeepsSameOriginRedirects(t *testing.T) {
	var received string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/finish" {
			http.Redirect(w, r, "/finish", http.StatusTemporaryRedirect)
			return
		}
		received = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer server.Close()
	provider := NewOpenAIProvider("test", "model", server.URL, "private-key")
	if _, err := provider.CompleteTools(context.Background(), "system", nil, nil, "session"); err != nil {
		t.Fatal(err)
	}
	if received != "Bearer private-key" {
		t.Fatal("same-origin redirect lost provider authorization")
	}
}
