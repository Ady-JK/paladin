package claude

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fakeCLI(t *testing.T, scenario string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claude-fake")
	cmd := exec.Command("go", "build", "-o", path, "./testdata/fake")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fake: %v: %s", err, b)
	}
	t.Setenv(BinEnv, path)
	t.Setenv("PALADIN_CLAUDE_TEST_SCENARIO", scenario)
}
func nextResult(t *testing.T, c *Client) Raw {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		for {
			r, ok := c.sub.Next()
			if !ok {
				break
			}
			if r.Str("type") == "result" {
				return r
			}
		}
		select {
		case <-c.sub.Wake():
		case <-c.Done():
			t.Fatalf("closed before result: %v", c.Err())
		case <-deadline.C:
			t.Fatal("no result")
		}
	}
}
func TestClientMultipleTurnsAndControl(t *testing.T) {
	fakeCLI(t, "")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c, err := Start(ctx, Options{Cwd: t.TempDir(), Model: "claude-haiku-5-5", PermissionMode: "default"}, func(_ context.Context, r Raw) (any, error) {
		return map[string]any{"behavior": "allow", "updatedInput": r.Obj("input")}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if len(c.Models()) != 1 || c.Models()[0].ID != "claude-haiku-5-5" {
		t.Fatalf("models=%v", c.Models())
	}
	for _, text := range []string{"first", "second", "permission-test"} {
		if err = c.Prompt(text, "test-id"); err != nil {
			t.Fatal(err)
		}
		result := nextResult(t, c)
		if result.Bool("is_error") {
			t.Fatalf("error result %v", result)
		}
	}
	if _, err = c.Call(ctx, map[string]any{"subtype": "set_model", "model": "claude-haiku-5-5"}); err != nil {
		t.Fatal(err)
	}
}
func TestLargeFramesAreNotTruncatedAtScannerDefault(t *testing.T) {
	fakeCLI(t, "")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c, err := Start(ctx, Options{Cwd: t.TempDir()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Prompt(strings.Repeat("text ", 20000), "large"); err != nil {
		t.Fatal(err)
	}
	if result := nextResult(t, c); result.Bool("is_error") {
		t.Fatal("large NDJSON frame failed")
	}
}

func TestMalformedOutputStopsTransport(t *testing.T) {
	fakeCLI(t, "malformed")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c, err := Start(ctx, Options{Cwd: t.TempDir()}, nil)
	if c == nil {
		if err == nil {
			t.Fatal("missing initialization failure")
		}
		return
	}
	defer c.Close()
	select {
	case <-c.Done():
		if c.Err() == nil {
			t.Fatal("malformed frame was not reported")
		}
	case <-ctx.Done():
		t.Fatal("malformed output left process running")
	}
}
func TestBlockedWriteHonorsCancellation(t *testing.T) {
	fakeCLI(t, "blocked")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c, err := Start(ctx, Options{Cwd: t.TempDir()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	writeCtx, writeCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer writeCancel()
	_, err = c.Call(writeCtx, map[string]any{"subtype": "blocked", "data": strings.Repeat("x", 2*1024*1024)})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("write cancellation=%v", err)
	}
	closed := make(chan struct{})
	go func() { c.Close(); close(closed) }()
	select {
	case <-closed:
	case <-ctx.Done():
		t.Fatal("Close blocked behind pipe writer")
	}
}
func TestMissingCLIReportsInstallation(t *testing.T) {
	t.Setenv(BinEnv, filepath.Join(t.TempDir(), "missing"))
	if _, err := FindBinary(); err == nil {
		t.Fatal("missing binary accepted")
	}
}
