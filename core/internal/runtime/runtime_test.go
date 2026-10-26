package runtime

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/config"
	"github.com/Ady-JK/paladin/core/internal/turn"
)

func testRuntime(t *testing.T) (*AgentRuntime, string) {
	t.Helper()
	dir := t.TempDir()
	return WithKeyStore(dir, config.NewConfig(dir), config.NewMemoryKeyStore()), dir
}

func TestOpenListHistoryDeleteSession(t *testing.T) {
	rt, _ := testRuntime(t)
	if err := rt.store.Keys.Set("openrouter", "sk-test"); err != nil {
		t.Fatal(err)
	}
	sess, err := rt.OpenSession("", "/tmp/proj")
	if err != nil {
		t.Fatal(err)
	}
	if sess.SessionID == "" {
		t.Fatal("empty session id")
	}
	list, err := rt.ListSessions("/tmp/proj", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("got %d", len(list))
	}
	history, err := rt.SessionHistory(sess.SessionID, "/tmp/proj")
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 0 {
		t.Fatalf("got %d", len(history))
	}
	ok, err := rt.DeleteSession(sess.SessionID, "/tmp/proj")
	if err != nil || !ok {
		t.Fatalf("delete %v %v", ok, err)
	}
}

func TestConcurrentTurnRejected(t *testing.T) {
	rt, _ := testRuntime(t)
	noop := func() {}
	if !rt.tracker.track("s", "/tmp", noop) {
		t.Fatal("first track must succeed")
	}
	if rt.tracker.track("s", "/tmp", noop) {
		t.Fatal("second track must fail")
	}
	rt.tracker.untrack("s")
}

func TestFullTurnWithMockProvider(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices": [{"message": {"content": "done"}}]}`)
	}))
	defer server.Close()
	rt, _ := testRuntime(t)
	if err := rt.store.Keys.Set("openrouter", "sk-test"); err != nil {
		t.Fatal(err)
	}
	// Point openrouter at the mock server.
	rt.providersMu.Lock()
	file, err := rt.store.LoadProviders()
	if err != nil {
		t.Fatal(err)
	}
	for i, p := range file.Providers {
		if p.Name == "openrouter" {
			file.Providers[i].ApiURL = server.URL
		}
	}
	if err := rt.store.SaveProviders(file); err != nil {
		t.Fatal(err)
	}
	rt.providersMu.Unlock()
	sess, err := rt.OpenSession("", "/tmp/proj")
	if err != nil {
		t.Fatal(err)
	}
	if sess.Provider != "openrouter" {
		t.Fatalf("provider %q", sess.Provider)
	}
	tx := make(chan turn.ChatChunk, 64)
	done := make(chan struct{})
	defer close(done)
	req := turn.ChatRequest{SessionID: sess.SessionID, Message: "hi", Cwd: "/tmp/proj"}
	go func() {
		for range tx {
		}
	}()
	outcome, err := rt.Chat(req, tx, done)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Reply != "done" {
		t.Fatalf("reply %q", outcome.Reply)
	}
	history, err := rt.SessionHistory(sess.SessionID, "/tmp/proj")
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 {
		t.Fatalf("got %d", len(history))
	}
}
