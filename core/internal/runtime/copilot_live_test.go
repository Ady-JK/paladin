package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/agent"
	"github.com/Ady-JK/paladin/core/internal/config"
)

// The live turn reads a synthetic file in a temporary workspace only.
func TestCopilotLiveResponsesAgentTurn(t *testing.T) {
	if os.Getenv("PALADIN_COPILOT_LIVE_TEST") != "1" {
		t.Skip("set PALADIN_COPILOT_LIVE_TEST=1 to test the signed-in Copilot account")
	}
	raw, err := os.ReadFile(filepath.Join(config.DataDir(), "config", "providers.json"))
	if err != nil {
		t.Fatal(err)
	}
	var settings config.ProvidersFile
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatal(err)
	}
	base := ""
	for _, p := range settings.Providers {
		if p.Name == "github-copilot" && p.ApiType == config.ApiTypeCopilot {
			base = p.ApiURL
		}
	}
	if !strings.HasPrefix(base, "https://api.") || !strings.HasSuffix(base, ".githubcopilot.com") {
		t.Fatal("no trusted Copilot endpoint")
	}
	key, ok, err := (config.OsKeyStore{}).Get("github-copilot")
	if err != nil || !ok || key == "" {
		t.Fatal("Copilot sign-in unavailable")
	}
	t.Setenv("PALADIN_SNAPSHOTS", "off")
	rt, _ := testRuntime(t)
	_, err = rt.UpdateProvider(config.ProviderInput{Name: "github-copilot", ApiType: string(config.ApiTypeCopilot), ApiURL: base, ApiKey: key})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.SetDefaultProvider("github-copilot"); err != nil {
		t.Fatal(err)
	}
	result, err := rt.RefreshProvider("github-copilot")
	if err != nil || result.Error != "" {
		t.Fatalf("refresh: %+v, %v", result, err)
	}
	meta := rt.store.ModelMeta("github-copilot", "gpt-5.6-luna")
	if meta.Protocol != "responses" {
		t.Fatalf("model route = %q", meta.Protocol)
	}
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "sample.txt"), []byte("saffron-cobalt"), 0o600); err != nil {
		t.Fatal(err)
	}
	session, err := rt.OpenSession("", ws)
	if err != nil {
		t.Fatal(err)
	}
	model := "gpt-5.6-luna"
	out, chunks := chatToEnd(t, rt, agent.Request{SessionID: session.SessionID, Cwd: ws, Model: &model, Permissions: "read-only", Message: "Use the read tool to read sample.txt, then reply with its contents only."})
	if !strings.Contains(strings.ToLower(out.Reply), "saffron-cobalt") {
		t.Fatalf("unexpected reply %q", out.Reply)
	}
	for _, chunk := range chunks {
		if chunk.Tool != nil && chunk.Tool.Name == "read" {
			return
		}
	}
	t.Fatal("agent did not call the read tool")
}
