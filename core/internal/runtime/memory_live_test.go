package runtime

import (
	"os"
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/agent"
	"github.com/Ady-JK/paladin/core/internal/config"
	"github.com/Ady-JK/paladin/core/internal/memory"
)

// This opt-in test sends synthetic preferences to the local proxy. It never
// uses the user's real Paladin data or writes the proxy key to disk.
func TestLiveMemoryPreferences(t *testing.T) {
	if os.Getenv("PALADIN_LIVE_MEMORY_TEST") != "1" {
		t.Skip("set PALADIN_LIVE_MEMORY_TEST=1 to test the local LiteLLM proxy")
	}
	key := os.Getenv("LITELLM_MASTER_KEY")
	if key == "" {
		t.Fatal("LITELLM_MASTER_KEY is required")
	}
	rt, _ := testRuntime(t)
	_, err := rt.AddProvider(config.ProviderInput{Name: "local-test", ApiURL: "http://localhost:4000/v1", ApiType: "openai", ApiKey: key})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.SetDefaultProvider("local-test"); err != nil {
		t.Fatal(err)
	}
	workspace, other := t.TempDir(), t.TempDir()
	sess, err := rt.OpenSession("", workspace)
	if err != nil {
		t.Fatal(err)
	}
	turn := func(message string) {
		t.Helper()
		model := "claude-sonnet-5"
		out, _ := chatToEnd(t, rt, agent.Request{SessionID: sess.SessionID, Cwd: workspace, Message: message, Model: &model})
		if out.Stopped {
			t.Fatal("turn stopped")
		}
	}
	turn("For this project, I always want test names to start with the word Orbit. Acknowledge briefly.")
	project, err := rt.memory.Read(memory.TargetProject, workspace)
	if err != nil || !strings.Contains(strings.ToLower(strings.Join(project.Entries, " ")), "orbit") {
		t.Fatalf("project preference not captured: %+v, %v", project, err)
	}
	turn("Across all projects, I prefer concise answers with a short example. Acknowledge briefly.")
	user, err := rt.memory.Read(memory.TargetUser, workspace)
	if err != nil || !strings.Contains(strings.ToLower(strings.Join(user.Entries, " ")), "concise") {
		t.Fatalf("cross-project preference not captured: %+v, %v", user, err)
	}
	turn("On this machine across all projects, our standard build runner is called ZephyrRunner. Acknowledge briefly.")
	global, err := rt.memory.Read(memory.TargetGlobal, workspace)
	if err != nil || !strings.Contains(strings.ToLower(strings.Join(global.Entries, " ")), "zephyrrunner") {
		t.Fatalf("global environment fact not captured: %+v, %v", global, err)
	}
	turn("Correction for this project: use Comet instead of Orbit as the test name prefix from now on. Acknowledge briefly.")
	project, err = rt.memory.Read(memory.TargetProject, workspace)
	text := strings.ToLower(strings.Join(project.Entries, " "))
	if err != nil || !strings.Contains(text, "comet") || strings.Contains(text, "orbit") {
		t.Fatalf("project preference not corrected: %+v, %v", project, err)
	}
	turn("For this response only, say the word pineapple. This is a one-time request.")
	for _, target := range []memory.Target{memory.TargetUser, memory.TargetGlobal, memory.TargetProject} {
		res, err := rt.memory.Read(target, workspace)
		if err != nil || strings.Contains(strings.ToLower(strings.Join(res.Entries, " ")), "pineapple") {
			t.Fatalf("temporary instruction saved in %s: %+v, %v", target, res, err)
		}
	}
	for _, tc := range []struct {
		cwd     string
		project bool
	}{{workspace, true}, {other, false}} {
		fresh, err := rt.OpenSession("", tc.cwd)
		if err != nil {
			t.Fatal(err)
		}
		stored, err := rt.transcripts.Load(tc.cwd, fresh.SessionID)
		if err != nil {
			t.Fatal(err)
		}
		snapshot := strings.ToLower(rt.memory.SessionSnapshot(stored.Dir, tc.cwd))
		if !strings.Contains(snapshot, "concise") || !strings.Contains(snapshot, "zephyrrunner") || strings.Contains(snapshot, "comet") != tc.project {
			t.Fatalf("wrong memory in new chat (same project %v): %s", tc.project, snapshot)
		}
		if tc.project {
			model := "claude-sonnet-5"
			out, _ := chatToEnd(t, rt, agent.Request{SessionID: fresh.SessionID, Cwd: tc.cwd, Model: &model, Message: "What test name prefix do I prefer in this project? Answer briefly."})
			if !strings.Contains(strings.ToLower(out.Reply), "comet") {
				t.Fatalf("new chat did not use saved preference: %q", out.Reply)
			}
		}
	}
}
