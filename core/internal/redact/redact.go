// Package redact masks the exact secret values the app knows about: the
// provider keys the user stored. Everything else is the agent's job — the
// prompt tells it never to print secrets — so no pattern scan runs over
// tool output, logs or errors.

package redact

import (
	"sort"
	"strings"
	"sync"
)

const minRegistered = 8

var (
	mu         sync.RWMutex
	registered = map[string]bool{}
	ordered    []string
)

// String masks the exact registered values in s. It is the same cheap
// lookup as Registered; there is no pattern scanning.
func String(s string) string { return Registered(s) }

// Registered masks only the exact registered values (the user's stored
// keys). Every tool result goes through it: those values must never
// reach the model, whichever tool would carry them.
func Registered(s string) string {
	if s == "" {
		return s
	}
	mu.RLock()
	vals := ordered
	mu.RUnlock()
	for _, v := range vals {
		if strings.Contains(s, v) {
			s = strings.ReplaceAll(s, v, "[redacted]")
		}
	}
	return s
}

// Register adds an exact value to mask (e.g. a stored API key). Values
// shorter than 8 bytes are ignored: they would mask ordinary words.
func Register(v string) {
	v = strings.TrimSpace(v)
	if len(v) < minRegistered {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	if registered[v] {
		return
	}
	registered[v] = true
	rebuild()
}

// Unregister forgets a value passed to Register.
func Unregister(v string) {
	mu.Lock()
	defer mu.Unlock()
	delete(registered, strings.TrimSpace(v))
	rebuild()
}

// Reset forgets every registered value (tests).
func Reset() {
	mu.Lock()
	defer mu.Unlock()
	registered = map[string]bool{}
	ordered = nil
}

// rebuild orders values longest first so a value containing another is
// masked whole.
func rebuild() {
	out := make([]string, 0, len(registered))
	for v := range registered {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	ordered = out
}
