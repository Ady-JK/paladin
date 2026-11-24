package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCodexDiscoverySkipsMissingCLIAndRecoversWhenInstalled(t *testing.T) {
	rt, _, fake := codexRuntime(t, "chat")
	bin := os.Getenv("PALADIN_CODEX_BIN")
	t.Setenv("PALADIN_CODEX_BIN", filepath.Join(t.TempDir(), "missing-codex"))
	if rt.CodexStatus(false).Installed {
		t.Fatal("missing CLI reported as installed")
	}
	if err := rt.discoverCodexSessions(context.Background()); err != nil {
		t.Fatalf("missing optional CLI should skip discovery: %v", err)
	}
	if rt.codex.Live() != nil || fakeLog(t, fake) != "" || len(rt.codexThreadSessions()) != 0 {
		t.Fatal("discovery attempted to start or import Codex without the CLI")
	}
	if err := rt.store.Keys.Set("openrouter", "sk-test"); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.OpenSession("", t.TempDir()); err != nil {
		t.Fatalf("Paladin sessions must still open: %v", err)
	}
	t.Setenv("PALADIN_CODEX_BIN", bin)
	t.Setenv("PALADIN_FAKE_CODEX_CWD", t.TempDir())
	if err := rt.discoverCodexSessions(context.Background()); err != nil {
		t.Fatalf("discovery did not recover after installing the CLI: %v", err)
	}
	if len(rt.codexThreadSessions()) == 0 {
		t.Fatal("no threads discovered after installing the CLI")
	}
}

func TestCodexDiscoveryHandlesBrokenCLI(t *testing.T) {
	rt, _, _ := codexRuntime(t, "chat")
	path := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PALADIN_CODEX_BIN", path)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := rt.discoverCodexSessions(ctx); err == nil {
		t.Fatal("broken CLI startup error was lost")
	}
	if err := rt.store.Keys.Set("openrouter", "sk-test"); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.OpenSession("", t.TempDir()); err != nil {
		t.Fatalf("broken optional CLI prevented opening a Paladin session: %v", err)
	}
}

func TestCodexDiscoveryStartsInBackgroundAndLoadsSavedThreads(t *testing.T) {
	rt, log, fake := codexRuntime(t, "chat")
	cwd := t.TempDir()
	t.Setenv("PALADIN_FAKE_CODEX_CWD", cwd)
	start := time.Now()
	rt.StartCodexDiscovery()
	rt.StartCodexDiscovery() // multiple windows must share the worker
	if time.Since(start) > 500*time.Millisecond {
		t.Fatal("starting discovery blocked the caller")
	}
	log.waitFor(t, "sessions discovered", func(events []TurnEvent) bool {
		for _, e := range events {
			if e.Event == "sessions/changed" {
				return true
			}
		}
		return false
	})
	eventually(t, "Codex history loaded", func() bool {
		held := rt.codexThreadSessions()
		m, ok := held["th-old"]
		if !ok {
			return false
		}
		sess, err := rt.transcripts.Load(m.Cwd, m.SessionID)
		if err != nil {
			return false
		}
		rows, err := sess.History()
		return err == nil && len(rows) > 0 && rows[len(rows)-1].Content == "You are welcome."
	})
	if held := rt.codexThreadSessions(); len(held) != 2 {
		t.Fatalf("saved threads not discovered: %v", held)
	}
	if calls := fakeLog(t, fake); strings.Contains(calls, "thread/start") || strings.Contains(calls, "thread/resume") || strings.Contains(calls, "turn/start") {
		t.Fatalf("discovery started an agent instead of reading saved history: %s", calls)
	}
}

func TestCodexDiscoveryDoesNotRestoreDeletedSessions(t *testing.T) {
	rt, _, _ := codexRuntime(t, "chat")
	cwd := t.TempDir()
	t.Setenv("PALADIN_FAKE_CODEX_CWD", cwd)
	ctx := context.Background()
	if err := rt.discoverCodexSessions(ctx); err != nil {
		t.Fatal(err)
	}
	held := rt.codexThreadSessions()
	m := held["th-old"]
	if _, err := rt.DeleteSession(m.SessionID, m.Cwd); err != nil {
		t.Fatal(err)
	}
	if err := rt.discoverCodexSessions(ctx); err != nil {
		t.Fatal(err)
	}
	if _, found := rt.codexThreadSessions()["th-old"]; found {
		t.Fatal("background discovery restored a deleted session")
	}
	// An explicit resume remains available when the user wants it back.
	if _, err := rt.ImportCodexThread("th-old", cwd); err != nil {
		t.Fatal(err)
	}
	if _, found := rt.codexThreadSessions()["th-old"]; !found {
		t.Fatal("explicit resume failed")
	}
}

func TestCodexDiscoveryReadsEveryPageAndKeepsMissingWorkspacesAsHistory(t *testing.T) {
	rt, _, _ := codexRuntime(t, "chat")
	missing := filepath.Join(t.TempDir(), "deleted-workspace")
	var threads []map[string]any
	for i := 0; i < 53; i++ {
		threads = append(threads, map[string]any{"id": fmt.Sprintf("th-page-%d", i), "cwd": missing, "preview": fmt.Sprintf("saved chat %d", i), "source": "cli", "createdAt": 1794000000})
	}
	path := filepath.Join(t.TempDir(), "threads.json")
	data, err := json.Marshal(threads)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PALADIN_FAKE_CODEX_THREADS", path)
	if err := rt.discoverCodexSessions(context.Background()); err != nil {
		t.Fatal(err)
	}
	held := rt.codexThreadSessions()
	if len(held) != len(threads) {
		t.Fatalf("discovered %d of %d sessions", len(held), len(threads))
	}
	if held["th-page-52"].CreatedAt.Unix() != 1794000000 {
		t.Fatal("old chats were dated as new imports")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("discovery recreated a deleted workspace: %v", err)
	}
	if err := rt.discoverCodexSessions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(rt.codexThreadSessions()) != len(threads) {
		t.Fatal("rescanning duplicated sessions")
	}
}

func TestCodexDiscoveryStopsCleanlyAtShutdown(t *testing.T) {
	rt, _, _ := codexRuntime(t, "chat")
	rt.StartCodexDiscovery()
	rt.ShutdownTurns(time.Second)
	rt.StartCodexDiscovery() // a late window cannot restart work after shutdown
	done := make(chan struct{})
	go func() { rt.codexDiscovery.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("discovery continued after shutdown")
	}
}

// A background scan syncs a thread's history only when Codex says it changed:
// an idle thread costs nothing on later scans.
func TestCodexDiscoverySyncsOnlyThreadsThatChanged(t *testing.T) {
	rt, _, _ := codexRuntime(t, "chat")
	cwd := t.TempDir()
	t.Setenv("PALADIN_FAKE_CODEX_CWD", cwd)
	threads := filepath.Join(t.TempDir(), "threads.json")
	write := func(updated int) {
		body := fmt.Sprintf(`[{"id":"th-old","preview":"Fix the bug","cwd":%q,"model":"fake-luna","updatedAt":%d,"createdAt":1,"source":"cli"}]`, cwd, updated)
		if err := os.WriteFile(threads, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PALADIN_FAKE_CODEX_THREADS", threads)
	write(100)
	var synced []string
	onDiscoverySync = func(id string) { synced = append(synced, id) }
	t.Cleanup(func() { onDiscoverySync = nil })
	scan := func() {
		if err := rt.discoverCodexSessions(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	scan()
	if len(synced) != 1 {
		t.Fatalf("the first scan synced %d threads", len(synced))
	}
	scan()
	if len(synced) != 1 {
		t.Fatal("an unchanged thread was synced again")
	}
	write(200)
	scan()
	if len(synced) != 2 {
		t.Fatal("a thread Codex updated was not synced")
	}
}
