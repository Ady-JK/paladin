package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestStandaloneTerminalStdioProtocol(t *testing.T) {
	h := startHost(t, buildHost(t), t.TempDir())
	dir := t.TempDir()
	started := h.call("start", "terminalStart", map[string]any{"sessionId": "__standalone_pane__", "cwd": dir, "cols": 80, "rows": 24})
	if started["ok"] != true {
		t.Fatalf("terminalStart: %v", started)
	}
	id := started["result"].(string)
	line := fmt.Sprintf(`{"id":"write","method":"terminalWrite","params":{"sessionId":"__standalone_pane__","terminalId":%q,"data":"printf 'rpc-pty\\n'\n"}}`+"\n", id)
	if _, err := h.stdin.Write([]byte(line)); err != nil {
		t.Fatal(err)
	}
	chunks := make(chan string, 64)
	readerDone := make(chan struct{})
	go func() {
		defer close(chunks)
		defer close(readerDone)
		var seen strings.Builder
		for h.scan.Scan() {
			var msg map[string]any
			if json.Unmarshal(h.scan.Bytes(), &msg) != nil || msg["event"] != "terminal/data" || msg["terminalId"] != id {
				continue
			}
			chunk, _ := base64.StdEncoding.DecodeString(msg["data"].(string))
			chunks <- string(chunk)
			seen.Write(chunk)
			if strings.Contains(seen.String(), "rpc-pty\r\n") {
				return // Leave the scanner to h.call for terminalClose.
			}
		}
	}()
	var output strings.Builder
	deadline := time.After(15 * time.Second)
	// Linux sh may print its prompt directly before the command output,
	// while macOS zsh often inserts a newline first. The echoed command
	// contains a literal backslash-n, so this matches only actual output.
	for !strings.Contains(output.String(), "rpc-pty\r\n") {
		select {
		case chunk, ok := <-chunks:
			if !ok {
				t.Fatalf("terminal/data missing; received %q", output.String())
			}
			output.WriteString(chunk)
		case <-deadline:
			t.Fatalf("terminal/data timed out; received %q", output.String())
		}
	}
	<-readerDone
	if reply := h.call("close", "terminalClose", map[string]any{"sessionId": "__standalone_pane__", "terminalId": id}); reply["ok"] != true {
		t.Fatalf("terminalClose: %v", reply)
	}
}

func TestTerminalPTYStreamsAndAcceptsInput(t *testing.T) {
	t.Setenv("SHELL", "") // Cover the Linux /bin/sh and macOS /bin/zsh defaults.
	dir := t.TempDir()
	events := make(chan terminalEvent, 32)
	hub := newTerminalHub(func(e terminalEvent) { events <- e })
	defer hub.closeAll()
	id, err := hub.start("session-a", dir, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	if err := hub.write("session-b", id, "printf 'hello-pty\\n'\n"); err == nil {
		t.Fatal("other session wrote to terminal")
	}
	if err := hub.write("session-a", id, "printf 'hello-pty\\n'\n"); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case e := <-events:
			chunk, _ := base64.StdEncoding.DecodeString(e.Data)
			if e.TerminalID == id && strings.Contains(string(chunk), "hello-pty") {
				return
			}
		case <-deadline:
			t.Fatal("PTY output did not arrive")
		}
	}
}

func TestOneSessionCannotOpenEveryTerminal(t *testing.T) {
	hub := newTerminalHub(func(terminalEvent) {})
	for i := 0; i < maxTerminalsPerSession; i++ {
		hub.entries[fmt.Sprintf("t%d", i)] = &terminalProcess{sessionID: "busy"}
	}
	if _, err := hub.start("busy", t.TempDir(), 80, 24); err == nil || !strings.Contains(err.Error(), "this session") {
		t.Fatalf("err = %v, want a per-session limit", err)
	}
	// Another session is unaffected by the first one's terminals (it gets as
	// far as spawning, or fails there, but not on the limit).
	if _, err := hub.start("other", t.TempDir(), 80, 24); err != nil && strings.Contains(err.Error(), "this session") {
		t.Fatalf("other session hit the limit: %v", err)
	}
	for id, entry := range hub.entries {
		if entry.cmd == nil {
			delete(hub.entries, id) // the stand-ins have no process to stop
		}
	}
	hub.closeAll()
}

// A terminal that stops reading (a paste into a program that ignores its
// input) must not stall the caller: input is queued and written off the
// request loop.
func TestTerminalWriteNeverBlocksTheCaller(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	entry := &terminalProcess{sessionID: "s", file: w}
	done := make(chan error, 1)
	go func() {
		var last error
		for i := 0; i < 40; i++ { // far more than a pipe buffers
			last = entry.send(strings.Repeat("x", 32*1024))
		}
		done <- last
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("send blocked on a terminal that is not reading")
	}
}

func TestTerminalWriteDeliversQueuedInputInOrder(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	entry := &terminalProcess{sessionID: "s", file: w}
	for _, s := range []string{"one ", "two ", "three"} {
		if err := entry.send(s); err != nil {
			t.Fatal(err)
		}
	}
	buf := make([]byte, 13)
	if _, err := io.ReadFull(r, buf); err != nil || string(buf) != "one two three" {
		t.Fatalf("got %q, %v", buf, err)
	}
	_ = w.Close()
	_ = r.Close()
}
