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
)

func usd(v float64) *float64 { return &v }

func TestBudgetSetAndGet(t *testing.T) {
	rt, _ := testRuntime(t)
	if err := rt.store.Keys.Set("openrouter", "sk-test"); err != nil {
		t.Fatal(err)
	}
	pointMockAt(rt, "http://127.0.0.1:1")
	ws := t.TempDir()
	sess, err := rt.OpenSession("", ws)
	if err != nil {
		t.Fatal(err)
	}
	sid := sess.SessionID
	if v, err := rt.BudgetGet(ws, sid); err != nil || v.EffectiveUsd != 0 || v.SessionUsd != nil || v.DefaultUsd != 0 {
		t.Fatalf("fresh budget %+v %v", v, err)
	}
	if _, err := rt.BudgetSet(BudgetScopeGlobal, ws, sid, usd(2)); err != nil {
		t.Fatal(err)
	}
	if v, _ := rt.BudgetGet(ws, sid); v.EffectiveUsd != 2 || v.DefaultUsd != 2 {
		t.Fatalf("session must inherit the default: %+v", v)
	}
	if v, _ := rt.BudgetSet(BudgetScopeSession, ws, sid, usd(0.5)); v.EffectiveUsd != 0.5 || v.SessionUsd == nil || *v.SessionUsd != 0.5 {
		t.Fatalf("session budget must win: %+v", v)
	}
	if v, _ := rt.BudgetSet(BudgetScopeSession, ws, sid, usd(0)); v.EffectiveUsd != 0 {
		t.Fatalf("session 0 turns the limit off: %+v", v)
	}
	if v, _ := rt.BudgetSet(BudgetScopeSession, ws, sid, nil); v.EffectiveUsd != 2 || v.SessionUsd != nil {
		t.Fatalf("clearing the session budget inherits again: %+v", v)
	}
	if rt.turnBudget(ws, sid)() != 2 {
		t.Fatal("turn budget must read the effective limit")
	}
	if _, err := rt.BudgetSet(BudgetScopeSession, ws, sid, usd(-1)); err == nil {
		t.Fatal("negative budget accepted")
	}
	if _, err := rt.BudgetSet("planet", ws, sid, usd(1)); err == nil {
		t.Fatal("bad scope accepted")
	}
	if _, err := rt.BudgetSet(BudgetScopeSession, ws, "", usd(1)); err == nil {
		t.Fatal("session scope without a session accepted")
	}
	if v, err := rt.BudgetGet(ws, ""); err != nil || v.EffectiveUsd != 2 {
		t.Fatalf("global view %+v %v", v, err)
	}
	if v, _ := rt.BudgetSet(BudgetScopeGlobal, ws, "", nil); v.DefaultUsd != 0 {
		t.Fatalf("clearing the default %+v", v)
	}
}

func TestBudgetKeepsOtherSettings(t *testing.T) {
	rt, dir := testRuntime(t)
	path := filepath.Join(dir, "config", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"maxConcurrentTurns":3,"futureKey":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.BudgetSet(BudgetScopeGlobal, "", "", usd(1.5)); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `"futureKey": true`) || !strings.Contains(string(data), `"maxConcurrentTurns": 3`) {
		t.Fatalf("other settings lost: %s", data)
	}
	if rt.loadSettings().DefaultBudgetUsd != 1.5 {
		t.Fatal("default not stored")
	}
}

func TestBudgetEndsARealTurnWithASummary(t *testing.T) {
	ws := t.TempDir()
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		i := len(bodies)
		bodies = append(bodies, string(body))
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		if i == 0 {
			args := fmt.Sprintf(`{"path":%q}`, filepath.Join(ws, "missing.txt"))
			fmt.Fprintf(w, "data: %s\n\n", fmt.Sprintf(`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c0","function":{"name":"read","arguments":%q}}]}}]}`, args))
			fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":5,\"total_tokens\":10,\"cost\":0.75}}\n\ndata: [DONE]\n\n")
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"did: read; left: all\"}}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	rt, _ := testRuntime(t)
	if err := rt.store.Keys.Set("openrouter", "sk-test"); err != nil {
		t.Fatal(err)
	}
	pointMockAt(rt, srv.URL)
	sess, err := rt.OpenSession("", ws)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.BudgetSet(BudgetScopeSession, ws, sess.SessionID, usd(0.5)); err != nil {
		t.Fatal(err)
	}
	out, chunks := chatToEnd(t, rt, agent.Request{SessionID: sess.SessionID, Message: "go", Cwd: ws})
	if out.Reply != "did: read; left: all" {
		t.Fatalf("outcome %+v", out)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 || strings.Contains(bodies[1], `"tools"`) {
		t.Fatalf("want a second, tool-less call; got %d calls", len(bodies))
	}
	var notices []agent.BudgetNotice
	for _, c := range chunks {
		if c.Budget != nil {
			notices = append(notices, *c.Budget)
		}
	}
	if len(notices) != 1 || notices[0].State != agent.BudgetReached || notices[0].Spent != 0.75 {
		t.Fatalf("notices %+v", notices)
	}
	if v, _ := rt.BudgetGet(ws, sess.SessionID); v.SpentUsd != 0.75 || !v.HasCost || v.Calls != 1 {
		t.Fatalf("spend view %+v", v)
	}
}

func TestHubPublishesBudgetNotices(t *testing.T) {
	run := func(ctx context.Context, req agent.Request, gate func(func()), tx chan<- agent.ChatChunk, _ <-chan struct{}) (agent.ChatOutcome, error) {
		tx <- agent.ChatChunk{SessionID: req.SessionID, Budget: &agent.BudgetNotice{State: agent.BudgetReached, Spent: 1, Limit: 1}}
		gate(func() {})
		return agent.ChatOutcome{SessionID: req.SessionID}, nil
	}
	h, log := steerHub(t, run)
	if _, _, err := h.Submit(agent.Request{SessionID: "s1", Message: "go"}, SubmitOpts{}); err != nil {
		t.Fatal(err)
	}
	log.waitFor(t, "chat/budget", func(evs []TurnEvent) bool {
		for _, e := range evs {
			if e.Event == "chat/budget" && e.Budget != nil && e.Budget.State == agent.BudgetReached {
				return true
			}
		}
		return false
	})
}
