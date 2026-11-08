package runtime

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/agent"
	"github.com/Ady-JK/paladin/core/internal/config"
	"github.com/Ady-JK/paladin/core/internal/tools"
)

// This opt-in test uses synthetic chats and the local LiteLLM proxy.
func TestLiveSessionSearchFreshness(t *testing.T) {
	if os.Getenv("PALADIN_LIVE_SEARCH_TEST") != "1" {
		t.Skip("set PALADIN_LIVE_SEARCH_TEST=1 to use the local LiteLLM proxy")
	}
	key := os.Getenv("LITELLM_MASTER_KEY")
	if key == "" {
		t.Fatal("LITELLM_MASTER_KEY is required")
	}
	rt, _ := testRuntime(t)
	if _, err := rt.AddProvider(config.ProviderInput{Name: "local-test", ApiURL: "http://localhost:4000/v1", ApiType: "openai", ApiKey: key}); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.SetDefaultProvider("local-test"); err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	search, ok := rt.tools.Get("session_search")
	if !ok {
		t.Fatal("session_search missing")
	}
	if _, err := search.Call(json.RawMessage(`{"query":"lanternquartz"}`), &tools.ToolCtx{Workspace: ws}); err != nil {
		t.Fatal(err)
	}
	first, err := rt.OpenSession("", ws)
	if err != nil {
		t.Fatal(err)
	}
	model := "claude-sonnet-5"
	chatToEnd(t, rt, agent.Request{SessionID: first.SessionID, Cwd: ws, Model: &model, Message: "For this temporary test the code word is lanternquartz. Reply briefly."})
	second, err := rt.OpenSession("", ws)
	if err != nil {
		t.Fatal(err)
	}
	out, chunks := chatToEnd(t, rt, agent.Request{SessionID: second.SessionID, Cwd: ws, Model: &model, Message: "Use session_search to find the earlier chat containing lanternquartz, then report its code word."})
	called := false
	for _, chunk := range chunks {
		if chunk.Tool != nil && chunk.Tool.Name == "session_search" && strings.Contains(chunk.Tool.Result, first.SessionID) {
			called = true
		}
	}
	if !called || !strings.Contains(strings.ToLower(out.Reply), "lanternquartz") {
		t.Fatalf("fresh session search not used: called=%v reply=%q", called, out.Reply)
	}
}
