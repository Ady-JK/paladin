package runtime

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestStaleCacheLoadsOnceForConcurrentFirstCallers(t *testing.T) {
	var c staleCache[int]
	var loads atomic.Int32
	release := make(chan struct{})
	load := func() (int, error) {
		loads.Add(1)
		<-release
		return 7, nil
	}
	var wg sync.WaitGroup
	results := make([]int, 4)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], _ = c.get(time.Minute, load)
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	if loads.Load() != 1 {
		t.Fatalf("loads = %d, want 1", loads.Load())
	}
	for _, v := range results {
		if v != 7 {
			t.Fatalf("results = %v", results)
		}
	}
}

func TestStaleCacheServesTheOldValueWhileRefreshing(t *testing.T) {
	var c staleCache[int]
	if v, _ := c.get(time.Millisecond, func() (int, error) { return 1, nil }); v != 1 {
		t.Fatalf("first = %d", v)
	}
	time.Sleep(5 * time.Millisecond) // now stale
	gate := make(chan struct{})
	done := make(chan struct{})
	start := time.Now()
	v, err := c.get(time.Millisecond, func() (int, error) {
		defer close(done)
		<-gate
		return 2, nil
	})
	if err != nil || v != 1 {
		t.Fatalf("stale read = %d, %v; want the old value at once", v, err)
	}
	if time.Since(start) > 200*time.Millisecond {
		t.Fatalf("a stale read waited for the refresh")
	}
	close(gate)
	<-done
	deadline := time.Now().Add(time.Second)
	for {
		if v, _ := c.get(time.Hour, func() (int, error) { return 99, nil }); v == 2 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the refreshed value never replaced the old one")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestStaleCacheDropsAValueWhenARefreshFails(t *testing.T) {
	var c staleCache[int]
	_, _ = c.get(time.Millisecond, func() (int, error) { return 1, nil })
	time.Sleep(5 * time.Millisecond)
	failed := make(chan struct{})
	_, _ = c.get(time.Millisecond, func() (int, error) { defer close(failed); return 0, errors.New("signed out") })
	<-failed
	time.Sleep(20 * time.Millisecond)
	_, err := c.get(time.Hour, func() (int, error) { return 0, errors.New("signed out") })
	if err == nil {
		t.Fatal("a failed refresh must not keep serving the old value")
	}
}

func TestStaleCacheDoesNotKeepFailures(t *testing.T) {
	var c staleCache[int]
	if _, err := c.get(time.Hour, func() (int, error) { return 0, errors.New("boom") }); err == nil {
		t.Fatal("want the error")
	}
	if v, err := c.get(time.Hour, func() (int, error) { return 5, nil }); err != nil || v != 5 {
		t.Fatalf("after a failure the next call must load again, got %d, %v", v, err)
	}
}
