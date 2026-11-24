package codex

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func fakeBin(t *testing.T, body string) (bin, counter string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs a shell script")
	}
	dir := t.TempDir()
	counter = filepath.Join(dir, "starts")
	bin = filepath.Join(dir, "codex")
	// Only a real app-server start counts; the binary is also probed with --version.
	script := "#!/bin/sh\nif [ \"$1\" = app-server ]; then echo x >> " + counter + "; " + body + "; else echo codex-cli 0.0.0; fi\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PALADIN_CODEX_BIN", bin)
	return bin, counter
}

func starts(t *testing.T, counter string) int {
	data, _ := os.ReadFile(counter)
	return strings.Count(string(data), "x")
}

// A broken app-server is not respawned on every call: status polling would
// otherwise start a process per poll.
func TestManagerBacksOffAfterAFailedStart(t *testing.T) {
	_, counter := fakeBin(t, "exit 1")
	m := NewManager()
	for i := 0; i < 4; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if _, err := m.Client(ctx); err == nil {
			t.Fatal("a broken app-server started")
		}
		cancel()
	}
	if n := starts(t, counter); n != 1 {
		t.Fatalf("the app-server was started %d times", n)
	}
}

// Asking whether an app-server is live (and closing the manager) never waits
// for another caller's slow start.
func TestManagerLiveAndCloseDoNotWaitForAStart(t *testing.T) {
	fakeBin(t, "sleep 3")
	m := NewManager()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	go func() { _, _ = m.Client(ctx) }()
	time.Sleep(200 * time.Millisecond)
	done := make(chan struct{})
	go func() { m.Live(); m.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Live/Close waited for a start in progress")
	}
}
