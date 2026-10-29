package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// liveHost keeps every stdout line so tests can wait on responses and
// events independently.
type liveHost struct {
	t     *testing.T
	cmd   *exec.Cmd
	stdin interface {
		Write([]byte) (int, error)
		Close() error
	}
	mu     sync.Mutex
	lines  []map[string]any
	signal chan struct{}
	exited chan struct{}
	next   int
}

func startLiveHost(t *testing.T, bin, dataDir string) *liveHost {
	t.Helper()
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "PALADIN_DATA_DIR="+dataDir, "PALADIN_KEYSTORE=memory")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	h := &liveHost{t: t, cmd: cmd, stdin: stdin, signal: make(chan struct{}, 1), exited: make(chan struct{})}
	go func() {
		scan := bufio.NewScanner(stdout)
		scan.Buffer(make([]byte, 1024*1024), 16*1024*1024)
		for scan.Scan() {
			var msg map[string]any
			if json.Unmarshal(scan.Bytes(), &msg) != nil {
				continue
			}
			h.mu.Lock()
			h.lines = append(h.lines, msg)
			h.mu.Unlock()
			select {
			case h.signal <- struct{}{}:
			default:
			}
		}
		_ = cmd.Wait()
		close(h.exited)
	}()
	t.Cleanup(func() {
		_ = stdin.Close()
		select {
		case <-h.exited:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
		}
	})
	return h
}

func (h *liveHost) send(method string, params any) string {
	h.t.Helper()
	h.mu.Lock()
	h.next++
	id := fmt.Sprintf("r%d", h.next)
	h.mu.Unlock()
	raw, _ := json.Marshal(params)
	if _, err := fmt.Fprintf(h.stdin, `{"id":%q,"method":%q,"params":%s}`+"\n", id, method, raw); err != nil {
		h.t.Fatal(err)
	}
	return id
}

func (h *liveHost) waitLine(what string, pred func(map[string]any) bool) map[string]any {
	h.t.Helper()
	deadline := time.After(15 * time.Second)
	for {
		h.mu.Lock()
		for _, l := range h.lines {
			if pred(l) {
				h.mu.Unlock()
				return l
			}
		}
		h.mu.Unlock()
		select {
		case <-h.signal:
		case <-time.After(20 * time.Millisecond):
		case <-deadline:
			h.t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func (h *liveHost) call(method string, params any) map[string]any {
	h.t.Helper()
	id := h.send(method, params)
	msg := h.waitLine(method+" response", func(m map[string]any) bool { return m["id"] == id })
	if msg["ok"] != true {
		h.t.Fatalf("%s failed: %v", method, msg)
	}
	return msg
}

func (h *liveHost) event(event, sessionID string, extra func(map[string]any) bool) map[string]any {
	h.t.Helper()
	return h.waitLine(event+" for "+sessionID, func(m map[string]any) bool {
		return m["event"] == event && m["sessionId"] == sessionID && (extra == nil || extra(m))
	})
}

func turnMessage(m map[string]any) string {
	turn, _ := m["turn"].(map[string]any)
	s, _ := turn["message"].(string)
	return s
}

// holdModel streams "part", then waits for release before " done".
func holdModel(t *testing.T) (*httptest.Server, func()) {
	release := make(chan struct{})
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"part\"}}]}\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\" done\"}}]}\n\ndata: [DONE]\n\n")
	}))
	free := func() { once.Do(func() { close(release) }) }
	t.Cleanup(func() {
		free()
		srv.Close()
	})
	return srv, free
}

func setupLiveHost(t *testing.T) (*liveHost, func(), func() (string, string)) {
	t.Helper()
	srv, free := holdModel(t)
	h := startLiveHost(t, buildHost(t), t.TempDir())
	h.call("add_provider", map[string]string{"name": "mock", "apiUrl": srv.URL, "apiKey": "sk", "apiType": "openai"})
	h.call("set_default_provider", map[string]string{"name": "mock"})
	open := func() (string, string) {
		cwd := t.TempDir()
		res := h.call("open_session", map[string]string{"cwd": cwd})["result"].(map[string]any)
		return res["sessionId"].(string), cwd
	}
	return h, free, open
}

func TestStdioConcurrentSessionsQueueAndAttach(t *testing.T) {
	h, free, open := setupLiveHost(t)
	a, cwdA := open()
	b, cwdB := open()
	first := h.call("submit_chat", map[string]any{"sessionId": a, "message": "one", "cwd": cwdA, "display": "one!"})
	if st := first["result"].(map[string]any)["state"]; st != "running" {
		t.Fatalf("first turn should run: %v", first)
	}
	if st := h.call("submit_chat", map[string]any{"sessionId": b, "message": "two", "cwd": cwdB})["result"].(map[string]any)["state"]; st != "running" {
		t.Fatalf("other session should run concurrently, got %v", st)
	}
	if st := h.call("submit_chat", map[string]any{"sessionId": a, "message": "three", "cwd": cwdA})["result"].(map[string]any)["state"]; st != "queued" {
		t.Fatalf("follow-up should queue, got %v", st)
	}
	h.event("chat/delta", a, nil)
	h.event("chat/delta", b, nil)
	h.event("turn/queued", a, func(m map[string]any) bool { return turnMessage(m) == "three" })

	if turns := h.call("turns", map[string]any{})["result"].([]any); len(turns) != 3 {
		t.Fatalf("turns %v", turns)
	}
	att := h.call("attach", map[string]any{"sessionId": a})["result"].(map[string]any)
	turn, _ := att["turn"].(map[string]any)
	events, _ := att["events"].([]any)
	queued, _ := att["queued"].([]any)
	if turn["display"] != "one!" || len(events) != 1 || len(queued) != 1 || att["seq"].(float64) <= 0 {
		t.Fatalf("attach %v", att)
	}

	free()
	h.event("turn/done", b, nil)
	done := h.event("turn/done", a, func(m map[string]any) bool { return turnMessage(m) == "three" })
	if turn := done["turn"].(map[string]any); turn["reply"] != "part done" || turn["error"] != nil {
		t.Fatalf("done %v", done)
	}
	hist := h.call("session_history", map[string]any{"sessionId": a})["result"].([]any)
	if len(hist) != 4 {
		t.Fatalf("history %v", hist)
	}
}

func TestStdioCancelQueuedTurnAndStopDropsQueue(t *testing.T) {
	h, free, open := setupLiveHost(t)
	defer free()
	a, cwd := open()
	h.call("submit_chat", map[string]any{"sessionId": a, "message": "run", "cwd": cwd})
	q := h.call("submit_chat", map[string]any{"sessionId": a, "message": "later", "cwd": cwd})["result"].(map[string]any)
	h.call("submit_chat", map[string]any{"sessionId": a, "message": "much later", "cwd": cwd})
	if ok := h.call("cancel_turn", map[string]any{"turnId": q["turnId"]})["result"]; ok != true {
		t.Fatalf("cancel_turn %v", ok)
	}
	h.event("turn/done", a, func(m map[string]any) bool {
		turn := m["turn"].(map[string]any)
		return turn["message"] == "later" && turn["cancelled"] == true
	})
	h.event("chat/delta", a, nil)
	if ok := h.call("stop_chat", map[string]any{"sessionId": a})["result"]; ok != true {
		t.Fatalf("stop %v", ok)
	}
	h.event("turn/done", a, func(m map[string]any) bool {
		turn := m["turn"].(map[string]any)
		return turn["message"] == "much later" && turn["cancelled"] == true
	})
	h.event("turn/done", a, func(m map[string]any) bool {
		turn := m["turn"].(map[string]any)
		return turn["message"] == "run" && turn["stopped"] == true
	})
}

// The blocking chat method keeps its contract on top of the hub: events
// carry the request id as streamId and the final line answers it.
func TestStdioBlockingChatStillStreams(t *testing.T) {
	h, free, open := setupLiveHost(t)
	a, cwd := open()
	id := h.send("chat", map[string]any{"sessionId": a, "message": "hi", "cwd": cwd})
	h.event("chat/delta", a, func(m map[string]any) bool { return m["streamId"] == id })
	free()
	final := h.waitLine("chat response", func(m map[string]any) bool { return m["id"] == id })
	res, _ := final["result"].(map[string]any)
	if final["ok"] != true || res["reply"] != "part done" || res["sessionId"] != a {
		t.Fatalf("final %v", final)
	}
}

// Closing stdin stops running turns, persists them, and exits.
func TestStdioShutdownPersistsRunningTurns(t *testing.T) {
	srv, _ := holdModel(t)
	dir := t.TempDir()
	h := startLiveHost(t, buildHost(t), dir)
	h.call("add_provider", map[string]string{"name": "mock", "apiUrl": srv.URL, "apiKey": "sk", "apiType": "openai"})
	h.call("set_default_provider", map[string]string{"name": "mock"})
	cwd := t.TempDir()
	a := h.call("open_session", map[string]string{"cwd": cwd})["result"].(map[string]any)["sessionId"].(string)
	h.call("submit_chat", map[string]any{"sessionId": a, "message": "keep me", "cwd": cwd})
	h.event("chat/delta", a, nil)
	_ = h.stdin.Close()
	select {
	case <-h.exited:
	case <-time.After(10 * time.Second):
		t.Fatal("host did not exit after stdin closed")
	}
	h2 := startLiveHost(t, buildHost(t), dir)
	hist := h2.call("session_history", map[string]any{"sessionId": a})["result"].([]any)
	found := false
	for _, row := range hist {
		if m := row.(map[string]any); m["role"] == "user" && strings.Contains(fmt.Sprint(m["content"]), "keep me") {
			found = true
		}
	}
	if !found {
		t.Fatalf("stopped turn not persisted: %v (data %s)", hist, filepath.Base(dir))
	}
}

// SIGTERM (Electron's kill, a logout) persists running turns like EOF.
func TestStdioSigtermPersistsRunningTurns(t *testing.T) {
	srv, _ := holdModel(t)
	dir := t.TempDir()
	h := startLiveHost(t, buildHost(t), dir)
	h.call("add_provider", map[string]string{"name": "mock", "apiUrl": srv.URL, "apiKey": "sk", "apiType": "openai"})
	h.call("set_default_provider", map[string]string{"name": "mock"})
	cwd := t.TempDir()
	a := h.call("open_session", map[string]string{"cwd": cwd})["result"].(map[string]any)["sessionId"].(string)
	h.call("submit_chat", map[string]any{"sessionId": a, "message": "signal me", "cwd": cwd})
	h.event("chat/delta", a, nil)
	if err := h.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-h.exited:
	case <-time.After(10 * time.Second):
		t.Fatal("host did not exit after SIGTERM")
	}
	if code := h.cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("exit code %d, want a clean exit", code)
	}
	h2 := startLiveHost(t, buildHost(t), dir)
	hist := h2.call("session_history", map[string]any{"sessionId": a})["result"].([]any)
	if !strings.Contains(fmt.Sprint(hist), "signal me") {
		t.Fatalf("turn not persisted on SIGTERM: %v", hist)
	}
}
