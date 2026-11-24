package runtime

import (
	"sync"
	"testing"
	"time"
)

// Locks are per key: one session's slow snapshot does not hold up another's,
// while two operations on the same session still take turns.
func TestKeyedLockSerializesOnlyTheSameKey(t *testing.T) {
	var k keyedLock
	unlockA := k.lock("a")

	other := make(chan struct{})
	go func() { k.lock("b")(); close(other) }()
	select {
	case <-other:
	case <-time.After(2 * time.Second):
		t.Fatal("a different key waited for a held lock")
	}

	same := make(chan struct{})
	go func() { k.lock("a")(); close(same) }()
	select {
	case <-same:
		t.Fatal("the same key was not held")
	case <-time.After(50 * time.Millisecond):
	}
	unlockA()
	select {
	case <-same:
	case <-time.After(2 * time.Second):
		t.Fatal("the waiter never got the lock")
	}
}

func TestKeyedLockForgetsIdleKeys(t *testing.T) {
	var k keyedLock
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			k.lock("s")()
		}()
	}
	wg.Wait()
	k.lock("x")()
	k.mu.Lock()
	n := len(k.held)
	k.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d idle entries kept", n)
	}
}
