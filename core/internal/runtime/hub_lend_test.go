package runtime

import (
	"sync"
	"testing"
	"time"
)

// A turn blocked waiting on another session's work lends its slot, so
// even maxConcurrentTurns=1 cannot deadlock a parent waiting on a child.
func TestHubLendFreesTheOnlySlotForAQueuedChild(t *testing.T) {
	h, runs, log := newTestHub(t, 1)
	submit(t, h, "parent", "p1")
	child, _ := submit(t, h, "child", "c1")
	if child.State != TurnQueued {
		t.Fatalf("child should queue behind the only slot, got %s", child.State)
	}
	restore := h.Lend("parent")
	log.waitFor(t, "child start", func(evs []TurnEvent) bool { return hasEvent(evs, "turn/start", "c1") })
	runs.finish("c1")
	log.waitFor(t, "child done", func(evs []TurnEvent) bool { return hasEvent(evs, "turn/done", "c1") })
	restore()
	restore() // idempotent
	// Once restored, the parent holds the slot again: a new turn queues.
	other, _ := submit(t, h, "other", "o1")
	if other.State != TurnQueued {
		t.Fatalf("restored parent must count against the limit, got %s", other.State)
	}
	runs.finish("p1")
	log.waitFor(t, "other start", func(evs []TurnEvent) bool { return hasEvent(evs, "turn/start", "o1") })
	runs.finish("o1")
}

func TestHubLendWithoutARunningTurnIsANoop(t *testing.T) {
	h, _, _ := newTestHub(t, 1)
	h.Lend("nobody")()
}

func TestHubOnDoneSeesFinishedAndDroppedTurns(t *testing.T) {
	h, runs, _ := newTestHub(t, 1)
	var mu sync.Mutex
	seen := map[string]TurnInfo{}
	got := make(chan struct{}, 8)
	h.SetOnDone(func(info TurnInfo) {
		mu.Lock()
		seen[info.Message] = info
		mu.Unlock()
		got <- struct{}{}
	})
	submit(t, h, "A", "a1")
	_, _ = submit(t, h, "A", "a2")
	h.StopSession("A", "")
	_ = runs
	for i := 0; i < 2; i++ {
		select {
		case <-got:
		case <-time.After(3 * time.Second):
			t.Fatalf("onDone fired %d times", i)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if !seen["a2"].Cancelled || !seen["a1"].Stopped {
		t.Fatalf("seen = %+v", seen)
	}
}

func TestHubSettleDoneWaitsForASlowCallback(t *testing.T) {
	h, runs, _ := newTestHub(t, 1)
	if !h.SettleDone(time.Millisecond) {
		t.Fatal("an idle hub should settle at once")
	}
	release := make(chan struct{})
	var ran sync.WaitGroup
	ran.Add(1)
	h.SetOnDone(func(TurnInfo) {
		ran.Done()
		<-release
	})
	submit(t, h, "s", "t1")
	runs.finish("t1")
	ran.Wait()
	if h.SettleDone(20 * time.Millisecond) {
		t.Fatal("settled while a callback was still running")
	}
	close(release)
	if !h.SettleDone(time.Second) {
		t.Fatal("did not settle after the callback returned")
	}
}
