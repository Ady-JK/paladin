package codex

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

func bareClient(t *testing.T, cmd *exec.Cmd) *Client {
	t.Helper()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	setProcAttr(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	c := &Client{
		cmd: cmd, stdin: stdin, stderr: &tail{max: 256},
		pending: map[int64]chan reply{}, subs: map[string]*Subscription{}, loaded: map[string]bool{}, models: map[string]string{},
		done: make(chan struct{}),
	}
	t.Cleanup(c.Close)
	return c
}

// An app-server that stops reading its input must not hang the caller: the
// write gives up, the process is retired and every waiter is released.
func TestWriteToAnAppServerThatStoppedReadingFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs sleep")
	}
	old := writeTimeout
	writeTimeout = 300 * time.Millisecond
	t.Cleanup(func() { writeTimeout = old })
	c := bareClient(t, exec.Command("sleep", "30"))
	start := time.Now()
	err := c.Call(context.Background(), "x", map[string]string{"blob": strings.Repeat("a", 4<<20)}, nil)
	if err == nil || time.Since(start) > 5*time.Second {
		t.Fatalf("err=%v after %v", err, time.Since(start))
	}
	select {
	case <-c.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("the client was not retired after a stalled write")
	}
}

// When the read loop dies (a line over the limit) the client fails, so
// pending calls and subscriptions end instead of waiting on a dead stream.
func TestReadFailureFailsTheClient(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs sleep")
	}
	old := maxLineBytes
	maxLineBytes = 1024
	t.Cleanup(func() { maxLineBytes = old })
	c := bareClient(t, exec.Command("sleep", "30"))
	go c.read(strings.NewReader(strings.Repeat("x", 4096) + "\n"))
	select {
	case <-c.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("a dead read loop left the client running")
	}
	if err := c.Err(); err == nil || errors.Is(err, ErrClosed) {
		t.Fatalf("err = %v", err)
	}
}
