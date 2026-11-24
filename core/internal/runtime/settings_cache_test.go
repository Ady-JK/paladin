package runtime

import (
	"os"
	"testing"
	"time"
)

// loadSettings remembers what it parsed, and notices every change to the
// file: our own writes and a hand edit alike.
func TestLoadSettingsFollowsTheFile(t *testing.T) {
	rt, _ := testRuntime(t)
	if got := rt.loadSettings(); got.MaxSubagents != 0 {
		t.Fatalf("fresh settings: %+v", got)
	}
	if err := rt.updateSettings(func(s *Settings) { s.MaxSubagents = 3 }); err != nil {
		t.Fatal(err)
	}
	if got := rt.loadSettings(); got.MaxSubagents != 3 {
		t.Fatalf("after updateSettings: %d", got.MaxSubagents)
	}
	if got := rt.loadSettings(); got.MaxSubagents != 3 {
		t.Fatalf("second read: %d", got.MaxSubagents)
	}
	// A hand edit with a different size and time is picked up.
	if err := os.WriteFile(rt.settingsPath(), []byte(`{"maxSubagents": 7, "maxConcurrentTurns": 2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(rt.settingsPath(), future, future)
	if got := rt.loadSettings(); got.MaxSubagents != 7 {
		t.Fatalf("after a hand edit: %d", got.MaxSubagents)
	}
	// A broken file reads as empty settings, and fixing it recovers.
	if err := os.WriteFile(rt.settingsPath(), []byte(`{nope`), 0o600); err != nil {
		t.Fatal(err)
	}
	later := future.Add(2 * time.Second)
	_ = os.Chtimes(rt.settingsPath(), later, later)
	if got := rt.loadSettings(); got.MaxSubagents != 0 {
		t.Fatalf("broken file: %d", got.MaxSubagents)
	}
	if err := os.Remove(rt.settingsPath()); err != nil {
		t.Fatal(err)
	}
	if got := rt.loadSettings(); got.MaxSubagents != 0 {
		t.Fatalf("missing file: %d", got.MaxSubagents)
	}
}
