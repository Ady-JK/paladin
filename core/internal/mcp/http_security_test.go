package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPDoesNotForwardCustomHeadersAcrossOrigins(t *testing.T) {
	var leaked string
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = r.Header.Get("X-API-Key")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	}))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	transport, err := newHTTP(ServerConfig{URL: source.URL, Headers: map[string]string{"X-API-Key": "private"}}, func([]byte) {}, func(error) {})
	if err != nil {
		t.Fatal(err)
	}
	defer transport.close()
	_ = transport.send(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	if leaked != "" {
		t.Fatalf("custom credential was forwarded to a different origin")
	}
}

func TestHTTPKeepsSameOriginRedirects(t *testing.T) {
	var received string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/finish", http.StatusTemporaryRedirect)
			return
		}
		received = r.Header.Get("X-API-Key")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	}))
	defer server.Close()
	transport, err := newHTTP(ServerConfig{URL: server.URL + "/start", Headers: map[string]string{"X-API-Key": "private"}}, func([]byte) {}, func(error) {})
	if err != nil {
		t.Fatal(err)
	}
	defer transport.close()
	if err := transport.send(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)); err != nil {
		t.Fatal(err)
	}
	if received != "private" {
		t.Fatalf("same-origin redirect lost configured header")
	}
}
