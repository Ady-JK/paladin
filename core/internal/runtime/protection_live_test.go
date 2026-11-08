package runtime

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/agent"
	"github.com/Ady-JK/paladin/core/internal/config"
)

// Opt-in live coverage uses only a temporary folder and the local proxy.
func TestLiveSnapshotProtection(t *testing.T) {
	if os.Getenv("PALADIN_LIVE_SNAPSHOT_TEST") != "1" {
		t.Skip("set PALADIN_LIVE_SNAPSHOT_TEST=1 to use the local LiteLLM proxy")
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
	if err := os.WriteFile(filepath.Join(ws, ".gitignore"), []byte("ignored.env\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "ignored.env"), []byte("sample"), 0o600); err != nil {
		t.Fatal(err)
	}
	sess, err := rt.OpenSession("", ws)
	if err != nil {
		t.Fatal(err)
	}
	model := "claude-sonnet-5"
	_, chunks := chatToEnd(t, rt, agent.Request{SessionID: sess.SessionID, Cwd: ws, Model: &model, Message: "Reply with exactly hello."})
	found := false
	for _, chunk := range chunks {
		if chunk.Protection != nil && chunk.Protection.State == "partial" && slices.Contains(chunk.Protection.Excluded, "ignored.env") {
			found = true
		}
	}
	if !found {
		t.Fatal("live turn did not report excluded file")
	}
	rows, err := rt.SessionHistory(sess.SessionID, ws)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Role == "user" && row.Protection != nil && slices.Contains(row.Protection.Excluded, "ignored.env") {
			return
		}
	}
	t.Fatal("snapshot protection did not persist in history")
}
