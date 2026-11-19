package inference

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/config"
	"github.com/google/uuid"
)

// headerServer records the headers of every request it gets.
func headerServer(t *testing.T, contentType, reply string) (*httptest.Server, *[]http.Header) {
	t.Helper()
	var seen []http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Clone())
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

func userTurn(content string) []LlmMessage { return []LlmMessage{{Role: "user", Content: content}} }

func TestCopilotAgentCallCarriesRequestIdentity(t *testing.T) {
	t.Setenv("PALADIN_DATA_DIR", t.TempDir())
	srv, seen := headerServer(t, "text/event-stream", chatSSE(`{"content":"ok"}`))
	p := copilotProviderAt(t, srv, "gemini-3.8-flash")
	yes := true
	ctx := WithCallMeta(context.Background(), CallMeta{InteractionID: "turn-123", Kind: CallAgent, UserInitiated: &yes})
	runStreamCtx(t, ctx, p, userTurn("hi"))
	runStreamCtx(t, ctx, p, userTurn("hi again"))
	if len(*seen) != 2 {
		t.Fatalf("requests %d", len(*seen))
	}
	h, h2 := (*seen)[0], (*seen)[1]
	idPattern := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	if !idPattern.MatchString(h.Get("X-Request-Id")) || h.Get("X-Request-Id") == h2.Get("X-Request-Id") {
		t.Fatalf("X-Request-Id %q then %q", h.Get("X-Request-Id"), h2.Get("X-Request-Id"))
	}
	for key, want := range map[string]string{
		"X-Agent-Task-Id":        h.Get("X-Request-Id"),
		"X-Interaction-Id":       "turn-123",
		"X-Interaction-Type":     "conversation-agent",
		"Openai-Intent":          "conversation-agent",
		"X-Initiator":            "user",
		"X-Github-Api-Version":   "2026-08-01",
		"Copilot-Integration-Id": "vscode-chat",
	} {
		if got := h.Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	if _, err := uuid.Parse(h.Get("Vscode-Sessionid")); err != nil {
		t.Errorf("VScode-SessionId %q", h.Get("Vscode-Sessionid"))
	}
	if h.Get("Vscode-Machineid") == "" || h.Get("Vscode-Machineid") != h2.Get("Vscode-Machineid") {
		t.Errorf("VScode-MachineId must be stable: %q vs %q", h.Get("Vscode-Machineid"), h2.Get("Vscode-Machineid"))
	}
	if h.Get("Vscode-Sessionid") != h2.Get("Vscode-Sessionid") {
		t.Error("VScode-SessionId must be stable within the process")
	}
}

func TestCopilotInteractionTypeFollowsCallKind(t *testing.T) {
	t.Setenv("PALADIN_DATA_DIR", t.TempDir())
	srv, seen := headerServer(t, "text/event-stream", chatSSE(`{"content":"ok"}`))
	p := copilotProviderAt(t, srv, "gemini-3.8-flash")
	for kind, want := range map[CallKind]string{CallSubagent: "conversation-subagent", CallCompaction: "conversation-compaction"} {
		no := false
		runStreamCtx(t, WithCallMeta(context.Background(), CallMeta{Kind: kind, UserInitiated: &no}), p, userTurn("x"))
		last := (*seen)[len(*seen)-1]
		if last.Get("X-Interaction-Type") != want || last.Get("X-Initiator") != "agent" {
			t.Errorf("%s: type %q initiator %q", kind, last.Get("X-Interaction-Type"), last.Get("X-Initiator"))
		}
	}
}

func TestCopilotInitiatorFallsBackToTheLastMessage(t *testing.T) {
	t.Setenv("PALADIN_DATA_DIR", t.TempDir())
	srv, seen := headerServer(t, "text/event-stream", chatSSE(`{"content":"ok"}`))
	p := copilotProviderAt(t, srv, "gemini-3.8-flash")
	id, name := "c1", "read"
	// A follow-up prompt late in a session: earlier assistant turns exist, but the
	// request is the user's, so it must not be sent as an agent round.
	runStream(t, p, []LlmMessage{
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "done"},
		{Role: "user", Content: "second"},
	})
	runStream(t, p, []LlmMessage{
		{Role: "user", Content: "go"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: id, Name: name, Arguments: "{}"}}},
		{Role: "tool", Content: "out", ToolCallID: &id, Name: &name},
	})
	if got := (*seen)[0].Get("X-Initiator"); got != "user" {
		t.Errorf("follow-up prompt sent as %q", got)
	}
	if got := (*seen)[1].Get("X-Initiator"); got != "agent" {
		t.Errorf("tool round sent as %q", got)
	}
}

func TestCopilotResponsesCallCarriesRequestIdentity(t *testing.T) {
	t.Setenv("PALADIN_DATA_DIR", t.TempDir())
	srv, seen := headerServer(t, "text/event-stream", responsesDone(`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}`))
	p := copilotProviderAt(t, srv, "gpt-5.6-luna")
	yes := true
	runStreamCtx(t, WithCallMeta(context.Background(), CallMeta{InteractionID: "turn-9", Kind: CallAgent, UserInitiated: &yes}), p, userTurn("hi"))
	h := (*seen)[0]
	if h.Get("X-Interaction-Id") != "turn-9" || h.Get("Openai-Intent") != "conversation-agent" ||
		h.Get("X-Request-Id") == "" || h.Get("X-Initiator") != "user" || h.Get("X-Github-Api-Version") != "2026-08-01" {
		t.Fatalf("headers %v", h)
	}
}

func TestPlainProvidersGetNoCopilotIdentity(t *testing.T) {
	t.Setenv("PALADIN_DATA_DIR", t.TempDir())
	srv, seen := headerServer(t, "text/event-stream", chatSSE(`{"content":"ok"}`))
	runStream(t, NewOpenAIProvider("litellm", "claude-sonnet-5", srv.URL, "k"), userTurn("hi"))
	h := (*seen)[0]
	for _, key := range []string{"X-Request-Id", "X-Interaction-Id", "X-Interaction-Type", "X-Initiator", "Vscode-Sessionid", "Vscode-Machineid", "Openai-Intent", "Copilot-Integration-Id"} {
		if h.Get(key) != "" {
			t.Errorf("%s leaked to a non-Copilot provider: %q", key, h.Get(key))
		}
	}
}

// Copilot only describes the 1M-token window and its price tiers to clients
// that ask with an API version of 2026-06-01 or later; older versions get the
// flat 200k listing, which hides the long-context choice. The catalog and the
// requests that rely on its limits must therefore use the same, newer version.
func TestCopilotCatalogAsksForTheTieredListing(t *testing.T) {
	t.Setenv("PALADIN_DATA_DIR", t.TempDir())
	srv, seen := headerServer(t, "application/json", `{"data":[]}`)
	p, err := BuildProvider(config.ApiTypeCopilot, "github-copilot", "", srv.URL, "tok")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := p.(*OpenAIProvider).ListCatalog(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(*seen) == 0 {
		t.Fatal("no catalog request")
	}
	if got := (*seen)[0].Get("X-Github-Api-Version"); got != "2026-08-01" {
		t.Fatalf("catalog API version %q hides the long-context tiers", got)
	}
}
