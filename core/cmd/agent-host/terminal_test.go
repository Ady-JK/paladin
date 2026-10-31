package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestTerminalRunUsesWorkspaceAndKeepsOutput(t *testing.T) {
	t.Setenv("SHELL", "") // Exercise the platform fallback: zsh on macOS, sh on Linux.
	dir := t.TempDir()
	result, err := terminalRun("printf 'hello'; pwd; exit 7", dir)
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 7 || !strings.Contains(result.Output, "hello") || !strings.Contains(result.Output, dir) {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestTerminalStdioProtocol(t *testing.T) {
	h := startHost(t, buildHost(t), t.TempDir())
	dir := t.TempDir()
	if reply := h.call("run", "terminalRun", map[string]any{"cwd": dir, "command": "printf rpc-output"}); reply["ok"] != true {
		t.Fatalf("terminalRun: %v", reply)
	} else if got := reply["result"].(map[string]any)["output"]; got != "rpc-output" {
		t.Fatalf("terminalRun output = %v", got)
	}
	started := h.call("start", "terminalStart", map[string]any{"sessionId": "session-a", "cwd": dir, "cols": 80, "rows": 24})
	if started["ok"] != true {
		t.Fatalf("terminalStart: %v", started)
	}
	id := started["result"].(string)
	line := fmt.Sprintf(`{"id":"write","method":"terminalWrite","params":{"sessionId":"session-a","terminalId":%q,"data":"printf 'rpc-pty\\n'\n"}}`+"\n", id)
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
	if reply := h.call("close", "terminalClose", map[string]any{"sessionId": "session-a", "terminalId": id}); reply["ok"] != true {
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
