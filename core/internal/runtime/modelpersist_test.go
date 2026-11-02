// modelpersist_test.go covers last-used model memory: a turn's
// explicit provider/model choice is written back to the session meta,
// so reopening the session restores it instead of the creation-time
// default. Turns without an explicit choice leave the meta alone.

package runtime

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/agent"
)

func TestTurnPersistsLastUsedModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices": [{"message": {"content": "ok"}}]}`)
	}))
	defer server.Close()
	rt, _ := testRuntime(t)
	if err := rt.store.Keys.Set("openrouter", "sk-test"); err != nil {
		t.Fatal(err)
	}
	pointMockAt(rt, server.URL)
	sess, err := rt.OpenSession("", "/tmp/proj")
	if err != nil {
		t.Fatal(err)
	}
	if sess.Model == "switched-model" {
		t.Fatal("test setup collided with the switched model")
	}
	provider, model := "openrouter", "switched-model"
	outcome, _ := chatToEnd(t, rt, agent.Request{
		SessionID: sess.SessionID, Cwd: "/tmp/proj",
		Provider: &provider, Model: &model, Message: "hi",
	})
	if outcome.Reply != "ok" {
		t.Fatalf("got %+v", outcome)
	}
	reopened, err := rt.OpenSession(sess.SessionID, "/tmp/proj")
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Provider != provider || reopened.Model != model {
		t.Fatalf("meta not updated: %+v", reopened)
	}
	// A turn without an explicit choice keeps the stored one.
	_, _ = chatToEnd(t, rt, agent.Request{SessionID: sess.SessionID, Cwd: "/tmp/proj", Message: "again"})
	kept, err := rt.OpenSession(sess.SessionID, "/tmp/proj")
	if err != nil {
		t.Fatal(err)
	}
	if kept.Provider != provider || kept.Model != model {
		t.Fatalf("meta clobbered: %+v", kept)
	}
}
