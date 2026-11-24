package runtime

import (
	"sync"
	"time"
)

// staleCache holds one value that is slow to produce (it launches a CLI) and
// rarely changes. Once it has a value it answers at once: an old value is
// returned immediately and refreshed in the background, so a caller only ever
// waits for the very first load. Concurrent first callers share one load.
// Failures are never cached, and a failed refresh drops the old value so the
// next caller sees the error instead of a stale success.
type staleCache[T any] struct {
	mu         sync.Mutex
	loadMu     sync.Mutex
	val        T
	ok         bool
	at         time.Time
	refreshing bool
}

func (c *staleCache[T]) get(ttl time.Duration, load func() (T, error)) (T, error) {
	c.mu.Lock()
	if c.ok {
		v := c.val
		if time.Since(c.at) >= ttl && !c.refreshing {
			c.refreshing = true
			go c.refresh(load)
		}
		c.mu.Unlock()
		return v, nil
	}
	c.mu.Unlock()

	c.loadMu.Lock()
	defer c.loadMu.Unlock()
	c.mu.Lock()
	if c.ok {
		v := c.val
		c.mu.Unlock()
		return v, nil
	}
	c.mu.Unlock()
	v, err := load()
	if err != nil {
		return v, err
	}
	c.mu.Lock()
	c.val, c.ok, c.at = v, true, time.Now()
	c.mu.Unlock()
	return v, nil
}

func (c *staleCache[T]) refresh(load func() (T, error)) {
	v, err := load()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.refreshing = false
	if err != nil {
		var zero T
		c.val, c.ok = zero, false
		return
	}
	c.val, c.at = v, time.Now()
}
