package runtime

import (
	"strconv"
	"sync"
	"testing"
)

// blocking must read the live map under the book's lock: jobs start and
// finish on other goroutines while a revert preview runs.
func TestBlockingReadsLiveJobsUnderLock(t *testing.T) {
	b := newJobBook(nil)
	dir := t.TempDir()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 2000; i++ {
			id := "j" + strconv.Itoa(i)
			b.mu.Lock()
			b.live[id] = &JobRecord{ID: id, SessionID: "s", Command: "sleep"}
			b.mu.Unlock()
			b.mu.Lock()
			delete(b.live, id)
			b.mu.Unlock()
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 2000; i++ {
			_ = b.blocking("s", dir)
		}
	}()
	wg.Wait()
}
