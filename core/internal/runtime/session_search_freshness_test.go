package runtime

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/tools"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

func TestSessionSearchSeesChangesThroughoutRuntime(t *testing.T) {
	rt, _ := testRuntime(t)
	ws := t.TempDir()
	search, ok := rt.tools.Get("session_search")
	if !ok {
		t.Fatal("session_search missing")
	}
	query := func(word string) string {
		t.Helper()
		raw, _ := json.Marshal(map[string]string{"query": word})
		out, err := search.Call(raw, &tools.ToolCtx{Workspace: ws})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if strings.Contains(query("orion"), `"sessionId"`) {
		t.Fatal("unexpected initial match")
	}
	sess, err := rt.transcripts.Create(ws, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.Append(transcript.RoleUser, "orion nebula"); err != nil {
		t.Fatal(err)
	}
	if out := query("orion"); !strings.Contains(out, sess.Meta.SessionID) {
		t.Fatalf("new session missing: %s", out)
	}
	if err := sess.Append(transcript.RoleAssistant, "pegasus galaxy"); err != nil {
		t.Fatal(err)
	}
	if out := query("pegasus"); !strings.Contains(out, sess.Meta.SessionID) {
		t.Fatalf("new message missing: %s", out)
	}
	if err := rt.transcripts.Delete(ws, sess.Meta.SessionID); err != nil {
		t.Fatal(err)
	}
	if out := query("pegasus"); strings.Contains(out, sess.Meta.SessionID) {
		t.Fatalf("deleted session remains: %s", out)
	}
}

func TestConcurrentSessionSearchesSeeLatestSession(t *testing.T) {
	rt, _ := testRuntime(t)
	ws := t.TempDir()
	sess, err := rt.transcripts.Create(ws, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.Append(transcript.RoleUser, "concurrentfreshword"); err != nil {
		t.Fatal(err)
	}
	search, ok := rt.tools.Get("session_search")
	if !ok {
		t.Fatal("session_search missing")
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := search.Call(json.RawMessage(`{"query":"concurrentfreshword"}`), &tools.ToolCtx{Workspace: ws})
			if err != nil || !strings.Contains(out, sess.Meta.SessionID) {
				t.Errorf("concurrent search: %s %v", out, err)
			}
		}()
	}
	wg.Wait()
}
