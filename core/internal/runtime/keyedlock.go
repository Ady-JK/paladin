package runtime

import "sync"

// keyedLock is a mutex per key (a session id). An entry lives only while
// someone holds or waits for it, so a long-running process keeps none for
// the sessions it has finished with.
type keyedLock struct {
	mu   sync.Mutex
	held map[string]*keyedEntry
}

type keyedEntry struct {
	mu   sync.Mutex
	refs int
}

// lock takes key's mutex and returns the function that releases it.
func (k *keyedLock) lock(key string) func() {
	k.mu.Lock()
	if k.held == nil {
		k.held = map[string]*keyedEntry{}
	}
	e := k.held[key]
	if e == nil {
		e = &keyedEntry{}
		k.held[key] = e
	}
	e.refs++
	k.mu.Unlock()
	e.mu.Lock()
	return func() {
		e.mu.Unlock()
		k.mu.Lock()
		if e.refs--; e.refs == 0 {
			delete(k.held, key)
		}
		k.mu.Unlock()
	}
}
