//go:build unix

package runtime

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/agent"
	"github.com/Ady-JK/paladin/core/internal/hooks"
)

func TestHooksRPCsListEditToggleAndTrust(t *testing.T) {
	rt, _ := testRuntime(t)
	ws := t.TempDir()
	cfg := `{"hooks":{"Stop":[{"hooks":[{"command":"echo hi"}]}]}}`
	view, err := rt.HooksSetConfig(hooks.ScopeGlobal, ws, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Sources) != 4 || len(view.Sources[0].Handlers) != 1 || len(view.Events) == 0 {
		t.Fatalf("view %+v", view)
	}
	key := view.Sources[0].Handlers[0].Key
	view, err = rt.HooksToggle(ws, key, false)
	if err != nil || view.Sources[0].Handlers[0].Enabled {
		t.Fatalf("toggle %+v %v", view, err)
	}
	claude := filepath.Join(ws, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(claude), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claude, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	view, err = rt.HooksTrust(ws, claude, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range view.Sources {
		if s.Path == claude && !s.Trusted {
			t.Fatalf("claude settings not trusted: %+v", s)
		}
	}
	if _, err := rt.HooksSetConfig("nope", ws, cfg); err == nil {
		t.Fatal("bad scope must fail")
	}
}

// hookTurnServer answers every request with plain text and records bodies.
func hookTurnServer(t *testing.T) (*httptest.Server, func() []string) {
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(body))
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), bodies...)
	}
}

func TestSessionStartHookRunsOnceAndHookEventsReachTheHub(t *testing.T) {
	ws := t.TempDir()
	srv, bodies := hookTurnServer(t)
	rt, _ := testRuntime(t)
	if err := rt.store.Keys.Set("openrouter", "sk-test"); err != nil {
		t.Fatal(err)
	}
	pointMockAt(rt, srv.URL)
	log := filepath.Join(t.TempDir(), "starts")
	cfg := `{"hooks":{
	  "SessionStart":[{"hooks":[{"command":"cat | grep -o '\"source\":\"[a-z]*\"' >> ` + log + `; echo repo uses pnpm"}]}],
	  "UserPromptSubmit":[{"hooks":[{"command":"echo '{\"systemMessage\":\"prompt seen\"}'"}]}]}}`
	if _, err := rt.HooksSetConfig(hooks.ScopeGlobal, ws, cfg); err != nil {
		t.Fatal(err)
	}
	sess, err := rt.OpenSession("", ws)
	if err != nil {
		t.Fatal(err)
	}
	_, chunks := chatToEnd(t, rt, agent.Request{SessionID: sess.SessionID, Message: "one", Cwd: ws})
	chatToEnd(t, rt, agent.Request{SessionID: sess.SessionID, Message: "two", Cwd: ws})
	got, _ := os.ReadFile(log)
	if strings.TrimSpace(string(got)) != `"source":"startup"` {
		t.Fatalf("SessionStart runs once per session, got %q", got)
	}
	if b := bodies(); len(b) != 2 || !strings.Contains(b[0], "repo uses pnpm") {
		t.Fatalf("first request lacks SessionStart context: %v", b)
	}
	var notes []hooks.Note
	for _, c := range chunks {
		if c.Hook != nil {
			notes = append(notes, *c.Hook)
		}
	}
	if len(notes) != 2 {
		t.Fatalf("notes %+v", notes)
	}
}

func TestHubPublishesHookNotes(t *testing.T) {
	run := func(_ context.Context, req agent.Request, gate func(func()), tx chan<- agent.ChatChunk, _ <-chan struct{}) (agent.ChatOutcome, error) {
		tx <- agent.ChatChunk{SessionID: req.SessionID, Hook: &hooks.Note{Event: hooks.PreToolUse, Command: "x", Outcome: hooks.NoteBlocked, Message: "no"}}
		gate(func() {})
		return agent.ChatOutcome{SessionID: req.SessionID}, nil
	}
	h, log := steerHub(t, run)
	if _, _, err := h.Submit(agent.Request{SessionID: "s1", Message: "go"}, SubmitOpts{}); err != nil {
		t.Fatal(err)
	}
	log.waitFor(t, "chat/hook", func(evs []TurnEvent) bool {
		for _, e := range evs {
			if e.Event == "chat/hook" && e.Hook != nil && e.Hook.Outcome == hooks.NoteBlocked {
				return true
			}
		}
		return false
	})
}
