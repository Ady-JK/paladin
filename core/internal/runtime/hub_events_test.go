package runtime

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Ady-JK/paladin/core/internal/agent"
)

// within fails the test unless fn returns in d.
func within(t *testing.T, d time.Duration, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { fn(); close(done) }()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("%s blocked", what)
	}
}

// A slow transcript read during attach must not stall the hub: other
// sessions keep submitting, streaming and listing.
func TestAttachReadsHistoryOffTheHubLock(t *testing.T) {
	h, runs, log := newTestHub(t, 4)
	release := make(chan struct{})
	reading := make(chan struct{})
	go func() {
		_, _ = h.Attach("slow", func() (any, error) { close(reading); <-release; return nil, nil })
	}()
	<-reading
	within(t, time.Second, "submit during a slow attach", func() { submit(t, h, "other", "hi") })
	within(t, time.Second, "list during a slow attach", func() { h.List() })
	runs.waitStarted(t, "hi")
	log.waitFor(t, "delta", func(evs []TurnEvent) bool {
		for _, e := range evs {
			if e.Event == "chat/delta" && e.SessionID == "other" {
				return true
			}
		}
		return false
	})
	close(release)
	runs.finish("hi")
}

// Attach and the turn's persist exclude each other per session, so a
// snapshot never sees history and a replay log that overlap or miss.
func TestAttachWaitsForAnInFlightPersistOfItsSession(t *testing.T) {
	h, runs, _ := newTestHub(t, 4)
	writing := make(chan struct{})
	release := make(chan struct{})
	runs.behavior["work"] = func(_ context.Context, gate func(func() error), tx chan<- agent.ChatChunk) (agent.ChatOutcome, error) {
		tx <- agent.ChatChunk{Delta: "partial"}
		gate(func() error { close(writing); <-release; return nil })
		return agent.ChatOutcome{Reply: "partial"}, nil
	}
	_, done := submit(t, h, "s1", "work")
	<-writing

	attached := make(chan Attachment, 1)
	go func() {
		att, _ := h.Attach("s1", func() (any, error) { return "history", nil })
		attached <- att
	}()
	select {
	case <-attached:
		t.Fatal("attach read history while the turn was being written")
	case <-time.After(50 * time.Millisecond):
	}
	// Another session attaches freely meanwhile.
	within(t, time.Second, "attach of another session", func() { _, _ = h.Attach("s2", func() (any, error) { return nil, nil }) })
	close(release)
	att := <-attached
	if att.Turn != nil && (!att.Turn.Persisted || len(att.Events) != 0) {
		t.Fatalf("attach after persist must not replay the log: %+v", att)
	}
	<-done
}

// blockingSink holds every event until released.
type blockingSink struct {
	release chan struct{}
	mu      sync.Mutex
	got     []TurnEvent
}

func (b *blockingSink) sink(e TurnEvent) {
	<-b.release
	b.mu.Lock()
	b.got = append(b.got, e)
	b.mu.Unlock()
}

func (b *blockingSink) events() []TurnEvent {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]TurnEvent(nil), b.got...)
}

// A client that stops reading must not freeze the hub: turns still
// start, stream, finish and answer queries.
func TestStalledSinkDoesNotBlockTheHub(t *testing.T) {
	runs := newFakeRuns()
	h := NewHub(runs.run, func() int { return 4 })
	stalled := &blockingSink{release: make(chan struct{})}
	h.SetSink(stalled.sink)
	t.Cleanup(func() { h.Shutdown(2 * time.Second) })

	_, done := submit(t, h, "s1", "one")
	runs.waitStarted(t, "one")
	within(t, time.Second, "list with a stalled sink", func() { h.List() })
	within(t, time.Second, "attach with a stalled sink", func() { _, _ = h.Attach("s1", nil) })
	runs.finish("one")
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("a turn could not finish while the sink was stalled")
	}
	close(stalled.release)
	deadline := time.Now().Add(2 * time.Second)
	for !hasEvent(stalled.events(), "turn/done", "one") {
		if time.Now().After(deadline) {
			t.Fatalf("events after the sink resumed: %s", describe(stalled.events()))
		}
		time.Sleep(5 * time.Millisecond)
	}
	assertIncreasingSeq(t, stalled.events())
}

func assertIncreasingSeq(t *testing.T, evs []TurnEvent) {
	t.Helper()
	for i := 1; i < len(evs); i++ {
		if evs[i].Seq <= evs[i-1].Seq {
			t.Fatalf("seq out of order at %d: %d after %d", i, evs[i].Seq, evs[i-1].Seq)
		}
	}
}

// Past its bound the writer drops streaming chunks, never lifecycle,
// tool or approval events, and tells the client to resync once it
// catches up.
func TestLaggingWriterDropsStreamingAndAsksForResync(t *testing.T) {
	var seq uint64
	stalled := &blockingSink{release: make(chan struct{})}
	var w *eventWriter
	w = newEventWriter(eventLimits{MaxEvents: 10, MaxBytes: 1 << 20}, func() {
		seq++
		w.push(TurnEvent{Event: "host/resync", Seq: seq})
	})
	w.setSink(stalled.sink)
	push := func(e TurnEvent) { seq++; e.Seq = seq; w.push(e) }
	for i := range 50 {
		push(TurnEvent{Event: "chat/delta", SessionID: "s", Delta: fmt.Sprint(i)})
		if i == 25 {
			push(TurnEvent{Event: "chat/tool", SessionID: "s", Tool: &agent.ToolActivity{ID: "t1"}})
			push(TurnEvent{Event: "chat/approval", SessionID: "s"})
		}
	}
	push(TurnEvent{Event: "turn/done", SessionID: "s"})
	close(stalled.release)
	if !w.flush(2 * time.Second) {
		t.Fatal("writer did not drain")
	}
	evs := stalled.events()
	var deltas int
	kinds := map[string]int{}
	for _, e := range evs {
		kinds[e.Event]++
		if e.Event == "chat/delta" {
			deltas++
		}
	}
	if deltas >= 50 || deltas == 0 {
		t.Fatalf("deltas delivered = %d, want some dropped", deltas)
	}
	for _, k := range []string{"chat/tool", "chat/approval", "turn/done"} {
		if kinds[k] != 1 {
			t.Fatalf("%s delivered %d times: %v", k, kinds[k], kinds)
		}
	}
	if kinds["host/resync"] != 1 || evs[len(evs)-1].Event != "host/resync" {
		t.Fatalf("want one trailing host/resync, got %v", kinds)
	}
	assertIncreasingSeq(t, evs)
}

// Within its bound the writer delivers everything, in order, with no
// resync.
func TestWriterDeliversEverythingWithinItsBound(t *testing.T) {
	var got []string
	w := newEventWriter(eventLimits{MaxEvents: 100, MaxBytes: 1 << 20}, func() { t.Error("unexpected resync") })
	w.setSink(func(e TurnEvent) { got = append(got, e.Delta) })
	for i := range 50 {
		w.push(TurnEvent{Event: "chat/delta", Seq: uint64(i + 1), Delta: fmt.Sprint(i)})
	}
	if !w.flush(time.Second) {
		t.Fatal("writer did not drain")
	}
	if len(got) != 50 || got[0] != "0" || got[49] != "49" {
		t.Fatalf("got %s", strings.Join(got, ","))
	}
}

// A panicking sink loses that event only.
func TestWriterSurvivesAPanickingSink(t *testing.T) {
	var got []string
	w := newEventWriter(eventLimits{MaxEvents: 100, MaxBytes: 1 << 20}, func() {})
	w.setSink(func(e TurnEvent) {
		if e.Delta == "bad" {
			panic("sink")
		}
		got = append(got, e.Delta)
	})
	for i, d := range []string{"a", "bad", "b"} {
		w.push(TurnEvent{Event: "chat/delta", Seq: uint64(i + 1), Delta: d})
	}
	if !w.flush(time.Second) || strings.Join(got, "") != "ab" {
		t.Fatalf("got %v", got)
	}
}

// Shutdown flushes the events of the turns it stopped.
func TestShutdownFlushesEvents(t *testing.T) {
	runs := newFakeRuns()
	h := NewHub(runs.run, func() int { return 4 })
	log := newEventLog()
	h.SetSink(log.sink)
	submit(t, h, "s1", "one")
	runs.waitStarted(t, "one")
	h.Shutdown(2 * time.Second)
	if !hasEvent(log.all(), "turn/done", "one") {
		t.Fatalf("turn/done not flushed: %s", describe(log.all()))
	}
}
