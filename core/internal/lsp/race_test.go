package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// readLog returns the fake server's log lines.
func readLog(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

// A response that arrives while the connection shuts down must never
// be sent into a closed channel (which would panic the core).
func TestResponseDuringCloseDoesNotPanic(t *testing.T) {
	for range 200 {
		toServer, fromClient := io.Pipe()
		fromServer, toClient := io.Pipe()
		c := newConn(fromServer, fromClient, nil, nil)
		// The "server" answers every request at once.
		go func() {
			r := bufio.NewReader(toServer)
			for {
				body, err := readFrame(r)
				if err != nil {
					return
				}
				var m message
				_ = json.Unmarshal(body, &m)
				resp, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": nil})
				if _, err := fmt.Fprintf(toClient, "Content-Length: %d\r\n\r\n%s", len(resp), resp); err != nil {
					return
				}
			}
		}()
		var wg sync.WaitGroup
		for range 4 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				_ = c.call(ctx, "ping", nil, nil)
			}()
		}
		c.close()
		wg.Wait()
		toClient.Close()
		toServer.Close()
	}
}

// A broken server answering one id twice must not wedge the read loop.
func TestDuplicateResponseDoesNotBlockReads(t *testing.T) {
	fromServer, toClient := io.Pipe()
	got := make(chan string, 1)
	c := newConn(fromServer, io.Discard, func(method string, _ json.RawMessage) { got <- method }, nil)
	defer c.close()
	frame := func(v any) {
		body, _ := json.Marshal(v)
		fmt.Fprintf(toClient, "Content-Length: %d\r\n\r\n%s", len(body), body)
	}
	c.mu.Lock()
	c.pending[1] = make(chan message, 1)
	c.mu.Unlock()
	go func() {
		frame(map[string]any{"jsonrpc": "2.0", "id": 1, "result": nil})
		frame(map[string]any{"jsonrpc": "2.0", "id": 1, "result": nil})
		frame(map[string]any{"jsonrpc": "2.0", "method": "after"})
	}()
	select {
	case m := <-got:
		if m != "after" {
			t.Fatalf("got %q", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the read loop stopped after a duplicate response")
	}
}

// Warm-ups and diagnoses racing on one file send versions in order, and
// the last document the server saw is the file's final content.
func TestConcurrentSyncsStayInOrder(t *testing.T) {
	log := filepath.Join(t.TempDir(), "log")
	m, ws := fakeManager(t, "FAKE_LOG="+log)
	path := filepath.Join(ws, "a.fk")
	writeFile(t, path, "v0\n")
	if res := m.Diagnose(context.Background(), ws, path, nil); res.Server != "fake" || res.Timedout {
		t.Fatalf("first diagnose %+v", res)
	}
	var wg sync.WaitGroup
	var fileMu sync.Mutex
	for i := 1; i <= 20; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); m.Warm(ws, path, nil) }()
		go func() {
			defer wg.Done()
			fileMu.Lock()
			writeFile(t, path, "v"+strconv.Itoa(i)+"\n")
			fileMu.Unlock()
			m.Diagnose(context.Background(), ws, path, nil)
		}()
	}
	wg.Wait()
	writeFile(t, path, "final ERROR\n")
	res := m.Diagnose(context.Background(), ws, path, nil)
	if res.Errors != 1 || res.Timedout {
		t.Fatalf("final diagnose %+v", res)
	}
	m.Close() // waits for warm-ups, so the log is complete
	last, lastText := 0, ""
	for _, line := range readLog(t, log) {
		f := strings.Fields(line)
		if len(f) < 3 || f[0] != "doc" {
			continue
		}
		v, _ := strconv.Atoi(f[2])
		if v <= last {
			t.Fatalf("version %d sent after %d", v, last)
		}
		last, lastText = v, strings.Join(f[3:], " ")
	}
	if lastText != "final ERROR" {
		t.Fatalf("the server's last document is %q", lastText)
	}
}

// A file over the size limit can still be read in pages by the tools,
// but it is never started for or sent to a language server.
func TestOversizedFilesAreNotSent(t *testing.T) {
	log := filepath.Join(t.TempDir(), "log")
	m, ws := fakeManager(t, "FAKE_LOG="+log)
	path := filepath.Join(ws, "big.fk")
	writeFile(t, path, "ERROR\n"+strings.Repeat("x", maxDocBytes))
	m.Warm(ws, path, nil)
	if res := m.Diagnose(context.Background(), ws, path, nil); res.Server != "" || res.Errors != 0 {
		t.Fatalf("diagnose %+v", res)
	}
	m.Close()
	if lines := readLog(t, log); len(lines) != 1 || lines[0] != "" {
		t.Fatalf("the server saw %q", lines)
	}
	if st := m.Status(); len(st) != 0 {
		t.Fatalf("status %+v", st)
	}
}

// A warm-up racing Close never leaves a server running.
func TestWarmRacingCloseLeavesNoServer(t *testing.T) {
	log := filepath.Join(t.TempDir(), "log")
	for i := range 20 {
		m, ws := fakeManager(t, "FAKE_LOG="+log)
		path := filepath.Join(ws, "a.fk")
		writeFile(t, path, "ok\n")
		m.Warm(ws, path, nil)
		if i%2 == 1 {
			time.Sleep(time.Duration(i) * time.Millisecond)
		}
		m.Close()
		m.Warm(ws, path, nil)
		if res := m.Diagnose(context.Background(), ws, path, nil); res.Errors != 0 || len(m.Status()) != 0 {
			t.Fatalf("a closed manager answered %+v / %+v", res, m.Status())
		}
	}
	time.Sleep(200 * time.Millisecond)
	for _, line := range readLog(t, log) {
		if pid, ok := strings.CutPrefix(line, "pid "); ok {
			n, _ := strconv.Atoi(pid)
			if syscall.Kill(n, 0) == nil {
				t.Fatalf("server %d still running after Close", n)
			}
		}
	}
}
