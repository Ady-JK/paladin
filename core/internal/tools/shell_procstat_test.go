package tools

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Ady-JK/paladin/core/internal/procstat"
)

func withTracker(t *testing.T) *procstat.Tracker {
	t.Helper()
	tr := procstat.New(nil, 0)
	prev := procTracker
	procTracker = tr
	t.Cleanup(func() { procTracker = prev })
	return tr
}

func waitTracked(t *testing.T, tr *procstat.Tracker, session string, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if tr.Tracked()[session] == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("tracked[%s] = %d, want %d", session, tr.Tracked()[session], want)
}

func TestForegroundShellIsTrackedForItsSessionWhileRunning(t *testing.T) {
	tr := withTracker(t)
	ws := t.TempDir()
	ctx := &ToolCtx{Workspace: ws, SessionDir: t.TempDir(), Cancel: context.Background(), SessionID: "fg"}
	done := make(chan error, 1)
	go func() {
		_, err := NewShellTool().Call(json.RawMessage(`{"cmd":"sleep 0.4"}`), ctx)
		done <- err
	}()
	waitTracked(t, tr, "fg", 1)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if n := tr.Tracked()["fg"]; n != 0 {
		t.Fatalf("still tracked after exit: %d", n)
	}
}

func TestBackgroundShellIsTrackedUntilTheJobEnds(t *testing.T) {
	tr := withTracker(t)
	ctx := &ToolCtx{Workspace: t.TempDir(), SessionDir: t.TempDir(), Cancel: context.Background(), SessionID: "bg"}
	tool := NewShellTool()
	if _, err := tool.Call(json.RawMessage(`{"cmd":"sleep 0.3","background":true}`), ctx); err != nil {
		t.Fatal(err)
	}
	waitTracked(t, tr, "bg", 1)
	waitTracked(t, tr, "bg", 0)
}
