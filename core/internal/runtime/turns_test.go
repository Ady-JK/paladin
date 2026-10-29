package runtime

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Ady-JK/paladin/core/internal/agent"
	"github.com/Ady-JK/paladin/core/internal/session"
)

// blockingModel streams "partial <msg>" for each request, then holds the
// stream open until released (or the client goes away) and finishes with
// " end".
type blockingModel struct {
	server   *httptest.Server
	inFlight atomic.Int32
	peak     atomic.Int32
	release  chan struct{}
	once     sync.Once
}

func newBlockingModel(t *testing.T) *blockingModel {
	m := &blockingModel{release: make(chan struct{})}
	m.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := m.inFlight.Add(1)
		defer m.inFlight.Add(-1)
		for {
			p := m.peak.Load()
			if n <= p || m.peak.CompareAndSwap(p, n) {
				break
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-m.release:
		case <-r.Context().Done():
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\" end\"}}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(func() {
		m.releaseAll()
		m.server.Close()
	})
	return m
}

func (m *blockingModel) releaseAll() { m.once.Do(func() { close(m.release) }) }

func hubRuntime(t *testing.T, url string) (*AgentRuntime, string, *eventLog) {
	t.Helper()
	rt, dir := testRuntime(t)
	if err := rt.store.Keys.Set("openrouter", "sk-test"); err != nil {
		t.Fatal(err)
	}
	pointMockAt(rt, url)
	log := newEventLog()
	rt.SetTurnSink(log.sink)
	t.Cleanup(func() { rt.ShutdownTurns(3 * time.Second) })
	return rt, dir, log
}

func openIn(t *testing.T, rt *AgentRuntime) string {
	t.Helper()
	sess, err := rt.OpenSession("", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return sess.SessionID
}

func cwdOf(t *testing.T, rt *AgentRuntime, id string) string {
	t.Helper()
	s, err := rt.OpenSession(id, "")
	if err != nil {
		t.Fatal(err)
	}
	return s.Cwd
}

func countEvents(evs []TurnEvent, event string) int {
	n := 0
	for _, e := range evs {
		if e.Event == event {
			n++
		}
	}
	return n
}

func TestRuntimeRunsSessionsConcurrently(t *testing.T) {
	model := newBlockingModel(t)
	rt, _, log := hubRuntime(t, model.server.URL)
	a, b := openIn(t, rt), openIn(t, rt)
	_, doneA, err := rt.SubmitTurn(agent.Request{SessionID: a, Message: "one", Cwd: cwdOf(t, rt, a)}, SubmitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	_, doneB, err := rt.SubmitTurn(agent.Request{SessionID: b, Message: "two", Cwd: cwdOf(t, rt, b)}, SubmitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	log.waitFor(t, "both streaming", func(evs []TurnEvent) bool { return countEvents(evs, "chat/delta") >= 2 })
	if model.peak.Load() < 2 {
		t.Fatalf("model saw %d concurrent requests, want 2", model.peak.Load())
	}
	model.releaseAll()
	for _, d := range []<-chan TurnInfo{doneA, doneB} {
		if f := <-d; f.Error != "" || f.Reply != "partial end" {
			t.Fatalf("final %+v", f)
		}
	}
	for _, id := range []string{a, b} {
		hist, err := rt.SessionHistory(id, "")
		if err != nil || len(hist) != 2 {
			t.Fatalf("history %v %+v", err, hist)
		}
	}
}

func TestRuntimeQueuesFollowUpInSameSession(t *testing.T) {
	model := newBlockingModel(t)
	rt, _, log := hubRuntime(t, model.server.URL)
	a := openIn(t, rt)
	cwd := cwdOf(t, rt, a)
	rt.SubmitTurn(agent.Request{SessionID: a, Message: "first", Cwd: cwd}, SubmitOpts{})
	info, done, err := rt.SubmitTurn(agent.Request{SessionID: a, Message: "second", Cwd: cwd}, SubmitOpts{})
	if err != nil || info.State != TurnQueued {
		t.Fatalf("follow-up should queue: %+v %v", info, err)
	}
	model.releaseAll()
	if f := <-done; f.Error != "" {
		t.Fatalf("queued turn failed: %+v", f)
	}
	hist, _ := rt.SessionHistory(a, "")
	var users []string
	for _, m := range hist {
		if m.Role == "user" {
			users = append(users, m.Content)
		}
	}
	if strings.Join(users, ",") != "first,second" {
		t.Fatalf("users %v", users)
	}
	_ = log
}

func TestRuntimeAttachMidTurn(t *testing.T) {
	model := newBlockingModel(t)
	rt, _, log := hubRuntime(t, model.server.URL)
	a := openIn(t, rt)
	_, done, _ := rt.SubmitTurn(agent.Request{SessionID: a, Message: "go", Cwd: cwdOf(t, rt, a)}, SubmitOpts{Display: "go!"})
	log.waitFor(t, "delta", func(evs []TurnEvent) bool { return countEvents(evs, "chat/delta") >= 1 })
	att, err := rt.Attach(a, "")
	if err != nil {
		t.Fatal(err)
	}
	hist := att.History.([]session.HistoryMessage)
	if len(hist) != 0 {
		t.Fatalf("in-flight turn must not be in history yet: %+v", hist)
	}
	if att.Turn == nil || att.Turn.Display != "go!" || len(att.Events) != 1 || att.Events[0].Delta != "partial" {
		t.Fatalf("attach %+v", att)
	}
	model.releaseAll()
	<-done
	att, _ = rt.Attach(a, "")
	if att.Turn != nil || len(att.History.([]session.HistoryMessage)) != 2 {
		t.Fatalf("after done %+v", att)
	}
}

func TestRuntimeConcurrencyCapFromSettings(t *testing.T) {
	model := newBlockingModel(t)
	rt, dir, log := hubRuntime(t, model.server.URL)
	if err := os.MkdirAll(filepath.Join(dir, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config", "settings.json"), []byte(`{"maxConcurrentTurns":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	a, b := openIn(t, rt), openIn(t, rt)
	rt.SubmitTurn(agent.Request{SessionID: a, Message: "a", Cwd: cwdOf(t, rt, a)}, SubmitOpts{})
	info, done, _ := rt.SubmitTurn(agent.Request{SessionID: b, Message: "b", Cwd: cwdOf(t, rt, b)}, SubmitOpts{})
	if info.State != TurnQueued {
		t.Fatalf("cap 1 should queue the second session: %+v", info)
	}
	model.releaseAll()
	<-done
	if model.peak.Load() != 1 {
		t.Fatalf("peak concurrency %d", model.peak.Load())
	}
	_ = log
}

func TestMaxConcurrentTurnsDefaultsAndClamps(t *testing.T) {
	rt, dir := testRuntime(t)
	if got := rt.maxConcurrentTurns(); got != defaultConcurrentTurns {
		t.Fatalf("default %d", got)
	}
	_ = os.MkdirAll(filepath.Join(dir, "config"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "config", "settings.json"), []byte(`{"maxConcurrentTurns":999}`), 0o644)
	if got := rt.maxConcurrentTurns(); got != maxConcurrentTurnsCap {
		t.Fatalf("clamp %d", got)
	}
}

func TestRuntimeStopAndDeleteWhileRunning(t *testing.T) {
	model := newBlockingModel(t)
	rt, _, log := hubRuntime(t, model.server.URL)
	a := openIn(t, rt)
	cwd := cwdOf(t, rt, a)
	_, done, _ := rt.SubmitTurn(agent.Request{SessionID: a, Message: "x", Cwd: cwd}, SubmitOpts{})
	log.waitFor(t, "delta", func(evs []TurnEvent) bool { return countEvents(evs, "chat/delta") >= 1 })
	if !rt.StopIn(a, "") {
		t.Fatal("stop found nothing")
	}
	if f := <-done; !f.Stopped {
		t.Fatalf("want stopped %+v", f)
	}

	b := openIn(t, rt)
	_, doneB, _ := rt.SubmitTurn(agent.Request{SessionID: b, Message: "y", Cwd: cwdOf(t, rt, b)}, SubmitOpts{})
	log.waitFor(t, "b delta", func(evs []TurnEvent) bool { return countEvents(evs, "chat/delta") >= 2 })
	if ok, err := rt.DeleteSession(b, ""); !ok || err != nil {
		t.Fatalf("delete running session: %v %v", ok, err)
	}
	<-doneB
	if _, err := rt.SessionHistory(b, ""); err == nil {
		t.Fatal("deleted session came back (turn persisted after delete)")
	}
}
