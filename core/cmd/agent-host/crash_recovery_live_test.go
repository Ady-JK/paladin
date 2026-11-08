package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This opt-in test uses a temporary workspace and only the local proxy.
func TestLiveCrashRecovery(t *testing.T) {
	if os.Getenv("PALADIN_LIVE_CRASH_TEST") != "1" {
		t.Skip("set PALADIN_LIVE_CRASH_TEST=1 for local LiteLLM")
	}
	key := os.Getenv("LITELLM_MASTER_KEY")
	if key == "" {
		t.Fatal("LITELLM_MASTER_KEY is required")
	}
	bin, dataDir, cwd := buildHost(t), t.TempDir(), t.TempDir()
	h := startLiveHost(t, bin, dataDir)
	h.call("addProvider", map[string]string{"name": "local-test", "apiUrl": "http://localhost:4000/v1", "apiKey": key, "apiType": "openai"})
	h.call("setDefaultProvider", map[string]string{"name": "local-test"})
	sid := h.call("openSession", map[string]string{"cwd": cwd})["result"].(map[string]any)["sessionId"].(string)
	h.call("submitChat", map[string]any{"sessionId": sid, "cwd": cwd, "model": "claude-sonnet-5", "message": "For a test, run the shell command exactly: echo started > marker.txt; sleep 8", "permissions": "full-access"})
	deadline := time.Now().Add(45 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(cwd, "marker.txt")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("live model did not start the test command")
		}
		time.Sleep(25 * time.Millisecond)
	}
	if err := h.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-h.exited:
	case <-time.After(10 * time.Second):
		t.Fatal("sidecar did not stop")
	}
	h = startLiveHost(t, bin, dataDir)
	h.call("openSession", map[string]string{"sessionId": sid})
	raw, _ := json.Marshal(h.call("sessionHistory", map[string]string{"sessionId": sid})["result"])
	if !strings.Contains(string(raw), "may have run") || !strings.Contains(string(raw), "interrupted") {
		t.Fatalf("missing interruption record: %s", raw)
	}
}
