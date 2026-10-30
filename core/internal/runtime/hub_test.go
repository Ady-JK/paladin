package runtime

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Ady-JK/paladin/core/internal/agent"
	"github.com/Ady-JK/paladin/core/internal/permission"
	"github.com/Ady-JK/paladin/core/internal/session"
)

// fakeRuns scripts the hub's run function per message: each run blocks
// on its release channel (or ctx) so tests control interleaving.
type fakeRuns struct {
	mu       sync.Mutex
	started  []string
	release  map[string]chan struct{}
	behavior map[string]func(ctx context.Context, gate func(func() error), tx chan<- agent.ChatChunk) (agent.ChatOutcome, error)
}

func newFakeRuns() *fakeRuns {
	return &fakeRuns{release: map[string]chan struct{}{}, behavior: map[string]func(context.Context, func(func() error), chan<- agent.ChatChunk) (agent.ChatOutcome, error){}}
}

func (f *fakeRuns) gateFor(msg string) chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch, ok := f.release[msg]
	if !ok {
		ch = make(chan struct{})
		f.release[msg] = ch
	}
	return ch
}

func (f *fakeRuns) finish(msg string) { close(f.gateFor(msg)) }

func (f *fakeRuns) startedList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.started...)
}

func (f *fakeRuns) run(ctx context.Context, req agent.Request, gate func(func() error), tx chan<- agent.ChatChunk, _ <-chan struct{}) (agent.ChatOutcome, error) {
	f.mu.Lock()
	f.started = append(f.started, req.Message)
	b := f.behavior[req.Message]
	f.mu.Unlock()
	if b != nil {
		return b(ctx, gate, tx)
	}
	tx <- agent.ChatChunk{SessionID: req.SessionID, Delta: "reply to " + req.Message}
	select {
	case <-f.gateFor(req.Message):
	case <-ctx.Done():
		gate(func() error { return nil })
		return agent.ChatOutcome{SessionID: req.SessionID, Stopped: true}, nil
	}
	gate(func() error { return nil })
	return agent.ChatOutcome{SessionID: req.SessionID, Reply: "reply to " + req.Message}, nil
}

// waitStarted polls until exactly want (comma-joined, sorted for
// concurrent starts) has started.
func (f *fakeRuns) waitStarted(t *testing.T, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		got := f.startedList()
		sort.Strings(got)
		if strings.Join(got, ",") == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("started %v, want %s", got, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

type eventLog struct {
	mu     sync.Mutex
	events []TurnEvent
	signal chan struct{}
}

func newEventLog() *eventLog { return &eventLog{signal: make(chan struct{}, 1024)} }

func (l *eventLog) sink(e TurnEvent) {
	l.mu.Lock()
	l.events = append(l.events, e)
	l.mu.Unlock()
	select {
	case l.signal <- struct{}{}:
	default:
	}
}

func (l *eventLog) all() []TurnEvent {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]TurnEvent(nil), l.events...)
}

// waitFor polls until pred holds over the log or fails the test.
func (l *eventLog) waitFor(t *testing.T, what string, pred func([]TurnEvent) bool) []TurnEvent {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		evs := l.all()
		if pred(evs) {
			return evs
		}
		select {
		case <-l.signal:
		case <-time.After(10 * time.Millisecond):
		case <-deadline:
			t.Fatalf("timed out waiting for %s; events: %s", what, describe(evs))
		}
	}
}

func describe(evs []TurnEvent) string {
	var b strings.Builder
	for _, e := range evs {
		b.WriteString(e.Event + ":" + e.SessionID)
		if e.Turn != nil {
			b.WriteString("(" + e.Turn.Message + ")")
		}
		b.WriteString(" ")
	}
	return b.String()
}

func hasEvent(evs []TurnEvent, event, message string) bool {
	return findEvent(evs, event, message) != nil
}

func findEvent(evs []TurnEvent, event, message string) *TurnEvent {
	for i := range evs {
		if evs[i].Event == event && evs[i].Turn != nil && evs[i].Turn.Message == message {
			return &evs[i]
		}
	}
	return nil
}

func newTestHub(t *testing.T, limit int) (*Hub, *fakeRuns, *eventLog) {
	t.Helper()
	runs := newFakeRuns()
	log := newEventLog()
	h := NewHub(runs.run, func() int { return limit })
	h.SetSink(log.sink)
	t.Cleanup(func() { h.Shutdown(2 * time.Second) })
	return h, runs, log
}

func submit(t *testing.T, h *Hub, sessionID, message string) (TurnInfo, <-chan TurnInfo) {
	t.Helper()
	info, done, err := h.Submit(agent.Request{SessionID: sessionID, Message: message, Cwd: "/w"}, SubmitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	return info, done
}

func TestHubRunsDifferentSessionsConcurrently(t *testing.T) {
	h, runs, log := newTestHub(t, 4)
	a, _ := submit(t, h, "A", "a1")
	b, _ := submit(t, h, "B", "b1")
	if a.State != TurnRunning || b.State != TurnRunning {
		t.Fatalf("both should start at once: %s %s", a.State, b.State)
	}
	log.waitFor(t, "both deltas", func(evs []TurnEvent) bool {
		n := 0
		for _, e := range evs {
			if e.Event == "chat/delta" {
				n++
			}
		}
		return n == 2
	})
	runs.finish("a1")
	runs.finish("b1")
	log.waitFor(t, "both done", func(evs []TurnEvent) bool {
		return hasEvent(evs, "turn/done", "a1") && hasEvent(evs, "turn/done", "b1")
	})
}

func TestHubQueuesSameSessionInOrder(t *testing.T) {
	h, runs, log := newTestHub(t, 4)
	submit(t, h, "A", "first")
	second, _ := submit(t, h, "A", "second")
	third, _ := submit(t, h, "A", "third")
	if second.State != TurnQueued || third.State != TurnQueued {
		t.Fatalf("same-session follow-ups must queue: %s %s", second.State, third.State)
	}
	log.waitFor(t, "turn/queued", func(evs []TurnEvent) bool { return hasEvent(evs, "turn/queued", "second") })
	runs.finish("first")
	runs.waitStarted(t, "first,second")
	runs.finish("second")
	runs.finish("third")
	log.waitFor(t, "third done", func(evs []TurnEvent) bool { return hasEvent(evs, "turn/done", "third") })
	if got := runs.startedList(); strings.Join(got, ",") != "first,second,third" {
		t.Fatalf("order %v", got)
	}
}

func TestHubCapLimitsConcurrentTurns(t *testing.T) {
	h, runs, log := newTestHub(t, 2)
	submit(t, h, "A", "a")
	submit(t, h, "B", "b")
	c, _ := submit(t, h, "C", "c")
	if c.State != TurnQueued {
		t.Fatalf("third session must wait for a slot, got %s", c.State)
	}
	runs.waitStarted(t, "a,b")
	runs.finish("b")
	log.waitFor(t, "c starts", func(evs []TurnEvent) bool { return hasEvent(evs, "turn/start", "c") })
	runs.finish("a")
	runs.finish("c")
}

func TestHubStopSessionStopsRunningAndDropsQueue(t *testing.T) {
	h, runs, log := newTestHub(t, 4)
	submit(t, h, "A", "running")
	_, queuedDone := submit(t, h, "A", "queued")
	submit(t, h, "B", "other")
	if !h.StopSession("A", "") {
		t.Fatal("stop reported nothing running")
	}
	final := <-queuedDone
	if !final.Cancelled || final.State != TurnDone {
		t.Fatalf("queued turn should be cancelled: %+v", final)
	}
	evs := log.waitFor(t, "running stopped", func(evs []TurnEvent) bool { return hasEvent(evs, "turn/done", "running") })
	if e := findEvent(evs, "turn/done", "running"); !e.Turn.Stopped {
		t.Fatalf("running turn should report stopped: %+v", e.Turn)
	}
	for _, m := range runs.startedList() {
		if m == "queued" {
			t.Fatal("dropped turn must never run")
		}
	}
	if !h.Running("B") {
		t.Fatal("stopping A must not touch B")
	}
	runs.finish("other")
}

func TestHubStopRunningKeepsTheQueue(t *testing.T) {
	h, runs, log := newTestHub(t, 4)
	if h.StopRunning("A") {
		t.Fatal("nothing is running yet")
	}
	submit(t, h, "A", "running")
	submit(t, h, "A", "next")
	if !h.StopRunning("A") {
		t.Fatal("stop missed the running turn")
	}
	evs := log.waitFor(t, "next starts", func(evs []TurnEvent) bool { return hasEvent(evs, "turn/start", "next") })
	if e := findEvent(evs, "turn/done", "running"); e == nil || !e.Turn.Stopped {
		t.Fatalf("running turn should report stopped: %s", describe(evs))
	}
	runs.finish("next")
}

func TestHubCancelQueuedTurnOnly(t *testing.T) {
	h, runs, log := newTestHub(t, 4)
	submit(t, h, "A", "one")
	two, _ := submit(t, h, "A", "two")
	submit(t, h, "A", "three")
	if !h.Cancel(two.TurnID) {
		t.Fatal("cancel queued failed")
	}
	if h.Cancel(two.TurnID) {
		t.Fatal("second cancel must report nothing to cancel")
	}
	runs.finish("one")
	runs.finish("three")
	log.waitFor(t, "three done", func(evs []TurnEvent) bool { return hasEvent(evs, "turn/done", "three") })
	if got := strings.Join(runs.startedList(), ","); got != "one,three" {
		t.Fatalf("started %s", got)
	}
}

func TestHubAttachReplaysCoalescedEvents(t *testing.T) {
	h, runs, log := newTestHub(t, 4)
	step := make(chan struct{})
	runs.behavior["m"] = func(ctx context.Context, gate func(func() error), tx chan<- agent.ChatChunk) (agent.ChatOutcome, error) {
		tx <- agent.ChatChunk{Delta: "hel"}
		tx <- agent.ChatChunk{Delta: "lo"}
		tx <- agent.ChatChunk{Tool: &agent.ToolActivity{ID: "t1", Name: "read", Status: session.ToolRunning}}
		tx <- agent.ChatChunk{Tool: &agent.ToolActivity{ID: "t1", Name: "read", Status: session.ToolOK, Result: "x"}}
		tx <- agent.ChatChunk{Delta: " after"}
		<-step
		gate(func() error { return nil })
		return agent.ChatOutcome{Reply: "hello after"}, nil
	}
	info, _ := submit(t, h, "A", "m")
	log.waitFor(t, "after delta", func(evs []TurnEvent) bool {
		for _, e := range evs {
			if e.Delta == " after" {
				return true
			}
		}
		return false
	})
	att, err := h.Attach("A", func() (any, error) { return "history", nil })
	if err != nil {
		t.Fatal(err)
	}
	if att.Turn == nil || att.Turn.TurnID != info.TurnID || att.Turn.Persisted {
		t.Fatalf("turn %+v", att.Turn)
	}
	if att.History != "history" {
		t.Fatalf("history %v", att.History)
	}
	var kinds []string
	for _, e := range att.Events {
		switch {
		case e.Delta != "":
			kinds = append(kinds, "d:"+e.Delta)
		case e.Tool != nil:
			kinds = append(kinds, "t:"+e.Tool.Status)
		}
	}
	if got := strings.Join(kinds, "|"); got != "d:hello|t:ok|d: after" {
		t.Fatalf("replay %s", got)
	}
	h.events.flush(time.Second)
	last := log.all()[len(log.all())-1]
	if att.Seq != last.Seq {
		t.Fatalf("attach seq %d, last live seq %d", att.Seq, last.Seq)
	}
	close(step)
	log.waitFor(t, "done", func(evs []TurnEvent) bool { return hasEvent(evs, "turn/done", "m") })
	att, _ = h.Attach("A", func() (any, error) { return nil, nil })
	if att.Turn != nil || len(att.Events) != 0 {
		t.Fatalf("finished turn must not replay: %+v", att)
	}
}

// Once the transcript holds the turn, an attach must not also replay it.
func TestHubAttachAfterPersistSkipsReplay(t *testing.T) {
	h, runs, log := newTestHub(t, 4)
	hold := make(chan struct{})
	persisted := make(chan struct{})
	runs.behavior["m"] = func(ctx context.Context, gate func(func() error), tx chan<- agent.ChatChunk) (agent.ChatOutcome, error) {
		tx <- agent.ChatChunk{Delta: "text"}
		gate(func() error { return nil })
		close(persisted)
		<-hold
		return agent.ChatOutcome{Reply: "text"}, nil
	}
	submit(t, h, "A", "m")
	<-persisted
	att, err := h.Attach("A", func() (any, error) { return "h", nil })
	if err != nil {
		t.Fatal(err)
	}
	if att.Turn == nil || !att.Turn.Persisted || len(att.Events) != 0 {
		t.Fatalf("got %+v", att)
	}
	close(hold)
	log.waitFor(t, "done", func(evs []TurnEvent) bool { return hasEvent(evs, "turn/done", "m") })
}

func TestHubAttachListsQueuedTurns(t *testing.T) {
	h, runs, _ := newTestHub(t, 4)
	submit(t, h, "A", "one")
	submit(t, h, "A", "two")
	att, _ := h.Attach("A", func() (any, error) { return nil, nil })
	if len(att.Queued) != 1 || att.Queued[0].Message != "two" {
		t.Fatalf("queued %+v", att.Queued)
	}
	if got := h.List(); len(got) != 2 {
		t.Fatalf("list %+v", got)
	}
	runs.finish("one")
	runs.finish("two")
}

func TestHubSurvivesPanickingRun(t *testing.T) {
	h, runs, log := newTestHub(t, 4)
	runs.behavior["boom"] = func(context.Context, func(func() error), chan<- agent.ChatChunk) (agent.ChatOutcome, error) {
		panic("kaboom")
	}
	_, done := submit(t, h, "A", "boom")
	submit(t, h, "A", "next")
	final := <-done
	if !strings.Contains(final.Error, "kaboom") {
		t.Fatalf("panic should surface as the turn error: %+v", final)
	}
	log.waitFor(t, "next starts", func(evs []TurnEvent) bool { return hasEvent(evs, "turn/start", "next") })
	runs.finish("next")
}

func TestHubReportsRunErrors(t *testing.T) {
	h, runs, _ := newTestHub(t, 4)
	runs.behavior["bad"] = func(context.Context, func(func() error), chan<- agent.ChatChunk) (agent.ChatOutcome, error) {
		return agent.ChatOutcome{}, errors.New("provider exploded")
	}
	_, done := submit(t, h, "A", "bad")
	if final := <-done; final.Error != "provider exploded" || final.State != TurnDone {
		t.Fatalf("got %+v", final)
	}
}

// Only a transcript write that succeeded marks the turn persisted.
func TestHubPersistedOnlyWhenTheWriteSucceeds(t *testing.T) {
	h, runs, _ := newTestHub(t, 4)
	for msg, writeErr := range map[string]error{"saved": nil, "lost": errors.New("disk full")} {
		runs.behavior[msg] = func(_ context.Context, gate func(func() error), _ chan<- agent.ChatChunk) (agent.ChatOutcome, error) {
			gate(func() error { return writeErr })
			return agent.ChatOutcome{}, writeErr
		}
	}
	_, saved := submit(t, h, "A", "saved")
	if info := <-saved; !info.Persisted {
		t.Fatalf("saved turn: %+v", info)
	}
	_, lost := submit(t, h, "B", "lost")
	if info := <-lost; info.Persisted {
		t.Fatalf("a failed write reported persisted: %+v", info)
	}
}

func TestHubEventsAreSequencedAndTagged(t *testing.T) {
	h, runs, log := newTestHub(t, 4)
	info, done, err := h.Submit(agent.Request{SessionID: "A", Message: "m", Cwd: "/w"}, SubmitOpts{Display: "shown"})
	if err != nil {
		t.Fatal(err)
	}
	runs.finish("m")
	final := <-done
	if final.Reply != "reply to m" || final.Display != "shown" {
		t.Fatalf("final %+v", final)
	}
	h.events.flush(time.Second)
	var prev uint64
	for _, e := range log.all() {
		if e.Seq <= prev {
			t.Fatalf("seq not increasing: %s", describe(log.all()))
		}
		prev = e.Seq
		if e.SessionID != "A" || e.TurnID != info.TurnID {
			t.Fatalf("untagged event %+v", e)
		}
	}
}

func TestHubValidatesRequests(t *testing.T) {
	h, _, _ := newTestHub(t, 4)
	if _, _, err := h.Submit(agent.Request{Message: "x"}, SubmitOpts{}); err == nil {
		t.Fatal("missing session must fail")
	}
	if _, _, err := h.Submit(agent.Request{SessionID: "A"}, SubmitOpts{}); err == nil {
		t.Fatal("empty message must fail")
	}
}

func TestHubShutdownStopsEverything(t *testing.T) {
	runs := newFakeRuns()
	h := NewHub(runs.run, func() int { return 1 })
	_, d1, _ := h.Submit(agent.Request{SessionID: "A", Message: "a"}, SubmitOpts{})
	_, d2, _ := h.Submit(agent.Request{SessionID: "B", Message: "b"}, SubmitOpts{})
	h.Shutdown(2 * time.Second)
	if f := <-d1; !f.Stopped {
		t.Fatalf("running turn should stop: %+v", f)
	}
	if f := <-d2; !f.Cancelled {
		t.Fatalf("queued turn should cancel: %+v", f)
	}
	if _, _, err := h.Submit(agent.Request{SessionID: "C", Message: "c"}, SubmitOpts{}); err == nil {
		t.Fatal("submit after shutdown must fail")
	}
}

func TestHubApprovalEventsReplayAndFlagTheTurn(t *testing.T) {
	h, runs, log := newTestHub(t, 4)
	step := make(chan struct{})
	resolved := make(chan struct{})
	runs.behavior["m"] = func(ctx context.Context, gate func(func() error), tx chan<- agent.ChatChunk) (agent.ChatOutcome, error) {
		tx <- agent.ChatChunk{Approval: &permission.Approval{ID: "c1", Tool: "shell", Command: "npm i", State: permission.StatePending}}
		<-step
		tx <- agent.ChatChunk{Approval: &permission.Approval{ID: "c1", Tool: "shell", Command: "npm i", State: permission.StateApproved, Decision: "once"}}
		<-resolved
		gate(func() error { return nil })
		return agent.ChatOutcome{Reply: "ok"}, nil
	}
	submit(t, h, "A", "m")
	log.waitFor(t, "approval", func(evs []TurnEvent) bool {
		for _, e := range evs {
			if e.Event == "chat/approval" && e.Approval != nil && e.Approval.ID == "c1" {
				return true
			}
		}
		return false
	})
	att, _ := h.Attach("A", nil)
	if att.Turn == nil || !att.Turn.AwaitingApproval {
		t.Fatalf("turn should await approval: %+v", att.Turn)
	}
	if len(att.Events) != 1 || att.Events[0].Approval == nil || att.Events[0].Approval.State != permission.StatePending {
		t.Fatalf("replay %+v", att.Events)
	}
	close(step)
	log.waitFor(t, "approved", func(evs []TurnEvent) bool {
		for _, e := range evs {
			if e.Approval != nil && e.Approval.State == permission.StateApproved {
				return true
			}
		}
		return false
	})
	att, _ = h.Attach("A", nil)
	if att.Turn.AwaitingApproval || len(att.Events) != 1 || att.Events[0].Approval.State != permission.StateApproved {
		t.Fatalf("after resolve: turn %+v events %+v", att.Turn, att.Events)
	}
	close(resolved)
	log.waitFor(t, "done", func(evs []TurnEvent) bool { return hasEvent(evs, "turn/done", "m") })
}

func TestHubStreamsAndReplaysThinking(t *testing.T) {
	h, runs, log := newTestHub(t, 4)
	step := make(chan struct{})
	runs.behavior["m"] = func(ctx context.Context, gate func(func() error), tx chan<- agent.ChatChunk) (agent.ChatOutcome, error) {
		tx <- agent.ChatChunk{ThinkingDropped: "max"}
		tx <- agent.ChatChunk{Reasoning: "let me "}
		tx <- agent.ChatChunk{Reasoning: "think"}
		tx <- agent.ChatChunk{Delta: "ok"}
		<-step
		gate(func() error { return nil })
		return agent.ChatOutcome{Reply: "ok"}, nil
	}
	submit(t, h, "A", "m")
	log.waitFor(t, "delta", func(evs []TurnEvent) bool {
		for _, e := range evs {
			if e.Delta == "ok" {
				return true
			}
		}
		return false
	})
	att, err := h.Attach("A", func() (any, error) { return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, e := range att.Events {
		switch e.Event {
		case "chat/reasoning":
			kinds = append(kinds, "r:"+e.Reasoning)
		case "chat/thinkingDropped":
			kinds = append(kinds, "x:"+e.ThinkingDropped)
		case "chat/delta":
			kinds = append(kinds, "d:"+e.Delta)
		}
	}
	if got := strings.Join(kinds, "|"); got != "x:max|r:let me think|d:ok" {
		t.Fatalf("replay %s", got)
	}
	close(step)
}
