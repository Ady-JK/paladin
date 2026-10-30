package runtime

import (
	"fmt"
	"os"
	"runtime/debug"
	"sync"
	"time"
)

// eventLimits bound what the writer holds for a client that is not
// reading.
type eventLimits struct {
	MaxEvents int
	MaxBytes  int
}

var defaultEventLimits = eventLimits{MaxEvents: 20_000, MaxBytes: 8 << 20}

// eventWriter delivers hub events to the sink in order on its own
// goroutine, so a slow client never holds the hub lock. Past its bound
// it drops streaming chunks (text, thinking, retries, compaction and
// hook notes), never lifecycle, tool, approval, steer or budget events,
// and once it catches up calls resync so the client re-attaches and
// fetches what it missed.
type eventWriter struct {
	limits eventLimits
	resync func()

	mu      sync.Mutex
	sink    func(TurnEvent)
	queue   []TurnEvent
	bytes   int
	lagged  bool
	running bool
	idle    chan struct{} // closed when the drain goroutine exits
}

func newEventWriter(limits eventLimits, resync func()) *eventWriter {
	idle := make(chan struct{})
	close(idle)
	return &eventWriter{limits: limits, resync: resync, sink: func(TurnEvent) {}, idle: idle}
}

func (w *eventWriter) setSink(sink func(TurnEvent)) {
	if sink == nil {
		sink = func(TurnEvent) {}
	}
	w.mu.Lock()
	w.sink = sink
	w.mu.Unlock()
}

// push queues e. Callers push in seq order (the hub does so under its
// lock); push never blocks on the sink.
func (w *eventWriter) push(e TurnEvent) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if (len(w.queue) >= w.limits.MaxEvents || w.bytes >= w.limits.MaxBytes) && droppable(e.Event) {
		w.lagged = true
		return
	}
	w.queue = append(w.queue, e)
	w.bytes += eventSize(e)
	if !w.running {
		w.running = true
		w.idle = make(chan struct{})
		go w.drain(w.idle)
	}
}

func (w *eventWriter) drain(idle chan struct{}) {
	for {
		w.mu.Lock()
		if len(w.queue) == 0 {
			if !w.lagged {
				w.running = false
				close(idle)
				w.mu.Unlock()
				return
			}
			w.lagged = false
			w.mu.Unlock()
			// The resync event is pushed (in seq order) after everything
			// that was dropped.
			w.resync()
			continue
		}
		batch, sink := w.queue, w.sink
		w.queue, w.bytes = nil, 0
		w.mu.Unlock()
		for _, e := range batch {
			deliver(sink, e)
		}
	}
}

func deliver(sink func(TurnEvent), e TurnEvent) {
	defer func() {
		if p := recover(); p != nil {
			fmt.Fprintf(os.Stderr, "event sink panicked on %s: %v\n%s", e.Event, p, debug.Stack())
		}
	}()
	sink(e)
}

// flush waits up to timeout for everything queued to be delivered.
func (w *eventWriter) flush(timeout time.Duration) bool {
	w.mu.Lock()
	idle := w.idle
	w.mu.Unlock()
	select {
	case <-idle:
		return true
	case <-time.After(timeout):
		return false
	}
}

// droppable names the streaming events a lagging client can re-read
// from an attach snapshot.
func droppable(event string) bool {
	switch event {
	case "chat/delta", "chat/reasoning", "chat/retry", "chat/compact", "chat/hook":
		return true
	}
	return false
}

// eventSize approximates an event's encoded size.
func eventSize(e TurnEvent) int {
	const envelope = 160
	return envelope + len(e.Delta) + len(e.Reasoning)
}
