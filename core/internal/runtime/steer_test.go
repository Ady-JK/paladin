package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/Ady-JK/paladin/core/internal/agent"
)

// steerHub runs turns with run so tests can see req.Steer.
func steerHub(t *testing.T, run RunFunc) (*Hub, *eventLog) {
	t.Helper()
	log := newEventLog()
	h := NewHub(run, func() int { return 2 })
	h.SetSink(log.sink)
	t.Cleanup(func() { h.Shutdown(2 * time.Second) })
	return h, log
}

func TestSteerNeedsARunningTurn(t *testing.T) {
	h, _, _ := newTestHub(t, 1)
	if _, err := h.Steer("s1", "hello", ""); err == nil {
		t.Fatal("steer accepted with nothing running")
	}
	if _, err := h.Steer("", "hello", ""); err == nil {
		t.Fatal("blank session accepted")
	}
}

func TestSteerReachesTheRunningTurn(t *testing.T) {
	got := make(chan agent.SteerMessage, 1)
	run := func(ctx context.Context, req agent.Request, gate func(func()), tx chan<- agent.ChatChunk, _ <-chan struct{}) (agent.ChatOutcome, error) {
		for {
			if msgs := req.Steer(); len(msgs) > 0 {
				tx <- agent.ChatChunk{SessionID: req.SessionID, Steer: &msgs[0]}
				got <- msgs[0]
				break
			}
			select {
			case <-ctx.Done():
				return agent.ChatOutcome{Stopped: true}, nil
			case <-time.After(5 * time.Millisecond):
			}
		}
		gate(func() {})
		return agent.ChatOutcome{SessionID: req.SessionID}, nil
	}
	h, log := steerHub(t, run)
	info, _, err := h.Submit(agent.Request{SessionID: "s1", Message: "go"}, SubmitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Steer(" ", "x", ""); err == nil {
		t.Fatal("blank session accepted")
	}
	if _, err := h.Steer("s1", "  ", ""); err == nil {
		t.Fatal("blank message accepted")
	}
	steered, err := h.Steer("s1", "expanded @a.go", "@a.go")
	if err != nil || steered.TurnID != info.TurnID {
		t.Fatalf("steer %+v %v", steered, err)
	}
	if m := <-got; m.Message != "expanded @a.go" || m.Display != "@a.go" {
		t.Fatalf("delivered %+v", m)
	}
	evs := log.waitFor(t, "chat/steer", func(evs []TurnEvent) bool {
		for _, e := range evs {
			if e.Event == "chat/steer" && e.Steer != nil && e.Steer.Display == "@a.go" && e.TurnID == info.TurnID {
				return true
			}
		}
		return false
	})
	_ = evs
	log.waitFor(t, "turn/done", func(evs []TurnEvent) bool { return hasEvent(evs, "turn/done", "go") })
	if len(h.List()) != 0 {
		t.Fatalf("a delivered steer must not queue a follow-up: %+v", h.List())
	}
}

func TestSteerLeftOverQueuesAFollowUp(t *testing.T) {
	h, runs, log := newTestHub(t, 1)
	if _, _, err := h.Submit(agent.Request{SessionID: "s1", Message: "first", Cwd: "/w", Mode: "plan", Permissions: "read-only"}, SubmitOpts{}); err != nil {
		t.Fatal(err)
	}
	runs.waitStarted(t, "first")
	if _, _, err := h.Submit(agent.Request{SessionID: "s1", Message: "queued"}, SubmitOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Steer("s1", "late steer", "late"); err != nil {
		t.Fatal(err)
	}
	// The fake run never reads req.Steer, like a turn that already ended.
	runs.finish("first")
	evs := log.waitFor(t, "follow-up start", func(evs []TurnEvent) bool { return hasEvent(evs, "turn/start", "late steer") })
	start := findEvent(evs, "turn/start", "late steer")
	if start.Turn.Display != "late" || start.Turn.Cwd != "/w" || start.Turn.Mode != "plan" || start.Turn.Permissions != "read-only" {
		t.Fatalf("follow-up must inherit the turn's settings: %+v", start.Turn)
	}
	if hasEvent(evs, "turn/start", "queued") {
		t.Fatal("the steer follow-up must run before turns queued after it")
	}
	runs.finish("late steer")
	runs.finish("queued")
	log.waitFor(t, "queued done", func(evs []TurnEvent) bool { return hasEvent(evs, "turn/done", "queued") })
}

func TestSteerLeftOverDroppedOnStop(t *testing.T) {
	h, runs, log := newTestHub(t, 1)
	if _, _, err := h.Submit(agent.Request{SessionID: "s1", Message: "first"}, SubmitOpts{}); err != nil {
		t.Fatal(err)
	}
	runs.waitStarted(t, "first")
	if _, err := h.Steer("s1", "never mind", ""); err != nil {
		t.Fatal(err)
	}
	h.StopRunning("s1")
	log.waitFor(t, "turn/done", func(evs []TurnEvent) bool { return hasEvent(evs, "turn/done", "first") })
	time.Sleep(30 * time.Millisecond)
	if got := runs.startedList(); len(got) != 1 {
		t.Fatalf("stop must drop pending steers, started %v", got)
	}
}
