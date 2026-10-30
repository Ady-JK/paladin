package runtime

import (
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

func TestThinkingLevelIsStoredPerModel(t *testing.T) {
	rt, dir := testRuntime(t)
	path := filepath.Join(dir, "config", "settings.json")
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, []byte(`{"defaultBudgetUsd":2}`), 0o644)

	v, err := rt.ThinkingGet("openrouter", "claude-sonnet-5")
	if err != nil || len(v.Levels) != 6 || v.Level != "" || v.Provider != "openrouter" || v.Model != "claude-sonnet-5" {
		t.Fatalf("get %+v %v", v, err)
	}
	if v, err = rt.ThinkingSet("openrouter", "claude-sonnet-5", "high"); err != nil || v.Level != "high" {
		t.Fatalf("set %+v %v", v, err)
	}
	if other, _ := rt.ThinkingGet("openrouter", "claude-haiku-4-5"); other.Level != "" || len(other.Levels) != 4 {
		t.Fatalf("other model %+v", other)
	}
	if _, err := rt.ThinkingSet("openrouter", "claude-sonnet-5", "ultra"); err == nil {
		t.Fatal("unknown level stored")
	}
	if _, err := rt.ThinkingSet("openrouter", "gpt-4o-mini", "low"); err == nil {
		t.Fatal("level stored for a model without thinking")
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `"defaultBudgetUsd": 2`) || !strings.Contains(string(data), `"claude-sonnet-5": "high"`) {
		t.Fatalf("settings %s", data)
	}
	if v, _ = rt.ThinkingSet("openrouter", "claude-sonnet-5", ""); v.Level != "" {
		t.Fatalf("clear %+v", v)
	}
	if data, _ = os.ReadFile(path); strings.Contains(string(data), "thinkingLevels") {
		t.Fatalf("empty map left behind: %s", data)
	}
	if _, err := rt.ThinkingGet("nope", "m"); err == nil {
		t.Fatal("unknown provider")
	}
}

func TestATurnSendsTheModelsThinkingLevel(t *testing.T) {
	rt, _ := testRuntime(t)
	_ = rt.store.Keys.Set("openrouter", "sk-test")
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(raw))
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"hmm\"}}]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"done\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()
	pointMockAt(rt, srv.URL)
	if _, err := rt.ThinkingSet("openrouter", "claude-sonnet-5", "max"); err != nil {
		t.Fatal(err)
	}
	provider, model, ws := "openrouter", "claude-sonnet-5", t.TempDir()
	sess, err := rt.OpenSession("", ws)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := chatToEnd(t, rt, agent.Request{SessionID: sess.SessionID, Message: "hi", Cwd: ws, Provider: &provider, Model: &model})
	if out.Reply != "done" {
		t.Fatalf("outcome %+v", out)
	}
	if len(bodies) == 0 || !strings.Contains(bodies[0], `"reasoning_effort":"max"`) {
		t.Fatalf("bodies %v", bodies)
	}
	if _, err := rt.ThinkingSet("openrouter", "claude-sonnet-5", ""); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	bodies = nil
	mu.Unlock()
	chatToEnd(t, rt, agent.Request{SessionID: sess.SessionID, Message: "again", Cwd: ws, Provider: &provider, Model: &model})
	if len(bodies) == 0 || strings.Contains(bodies[0], "reasoning_effort") {
		t.Fatalf("cleared level still sent: %v", bodies)
	}
}
