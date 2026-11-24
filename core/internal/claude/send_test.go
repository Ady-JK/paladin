package claude

import (
	"context"
	"os/exec"
	"runtime"
	"testing"
)

// A turn whose context is already cancelled must fail its write without
// killing the CLI, which the next turn reuses.
func TestSendWithACancelledContextDoesNotKillTheProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs sleep")
	}
	cmd := exec.Command("sleep", "30")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	c := &Client{cmd: cmd, stdin: stdin, done: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.send(ctx, map[string]any{"type": "x"}); err == nil {
		t.Fatal("a cancelled send succeeded")
	}
	if err := stdin.Close(); err != nil {
		t.Fatalf("stdin was already closed: %v", err) // the cancelled send must not have closed it
	}
	if err := cmd.Process.Signal(syscallZero()); err != nil {
		t.Fatalf("the process was killed: %v", err)
	}
}
