package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/agent"
	"github.com/Ady-JK/paladin/core/internal/config"
)

// Opt-in integration against the user's local LiteLLM proxy. All writes use
// disposable directories; the external target is outside the agent workspace.
func TestLivePermissionModes(t *testing.T) {
	if os.Getenv("PALADIN_LIVE_PERMISSIONS_TEST") != "1" {
		t.Skip("set PALADIN_LIVE_PERMISSIONS_TEST=1 to use the local LiteLLM proxy")
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
	workspace := t.TempDir()
	target := filepath.Join(workspace, "permission-proof.txt")
	outside := filepath.Join(t.TempDir(), "external-proof.txt")
	if err := os.WriteFile(target, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	sess, err := rt.OpenSession("", workspace)
	if err != nil {
		t.Fatal(err)
	}
	model := "claude-haiku-4-5-20251001"
	_, chunks := chatToEnd(t, rt, agent.Request{SessionID: sess.SessionID, Cwd: workspace, Model: &model,
		Permissions: "read-only", Message: fmt.Sprintf("This is a permission integration test in disposable folders. Attempt exactly one write tool call to set %s to read-only-must-not-write. If permission is denied, stop immediately and report it. Do not try another tool or ask a question.", target)})
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "original" {
		t.Fatalf("read-only changed the file: %q, %v", data, err)
	}
	blocked := false
	for _, chunk := range chunks {
		blocked = blocked || (chunk.Tool != nil && chunk.Tool.Status == "failed" && strings.Contains(chunk.Tool.Result, "read-only"))
	}
	if !blocked {
		t.Fatal("the live model did not exercise a read-only denial")
	}
	_, chunks = chatToEnd(t, rt, agent.Request{SessionID: sess.SessionID, Cwd: workspace, Model: &model,
		Permissions: "full-access", Message: fmt.Sprintf("The test now uses Full Access. Use the write tool to set %s to exactly full-access-write. Then use the shell tool to run printf full-access-shell > '%s'. Both paths are disposable test files. Do not ask for approval or use other tools. Report completion.", target, strings.ReplaceAll(outside, "'", "'\\''"))})
	for _, proof := range []struct{ path, want string }{{target, "full-access-write"}, {outside, "full-access-shell"}} {
		data, err := os.ReadFile(proof.path)
		if err != nil || string(data) != proof.want {
			t.Fatalf("Full Access proof %s: %q, %v", filepath.Base(proof.path), data, err)
		}
	}
	for _, chunk := range chunks {
		if chunk.Approval != nil {
			t.Fatal("Full Access unexpectedly asked for approval")
		}
	}
	t.Log("Live proof: Read-Only denied a write; switching the same session to Full Access allowed file editing and a shell write outside the workspace without approval.")
}
