package main

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/Ady-JK/paladin/core/internal/runtime"
)

func TestSlowMethodsDoNotBlockTheRequestLoop(t *testing.T) {
	release := make(chan struct{})
	methods["testSlow"] = func(*runtime.AgentRuntime, args) (any, error) { <-release; return "slow", nil }
	asyncMethods["testSlow"] = true
	t.Cleanup(func() { delete(methods, "testSlow"); delete(asyncMethods, "testSlow") })

	rt := testRuntime(t)
	var mu sync.Mutex
	var order []string
	out := func(r Response) {
		mu.Lock()
		order = append(order, string(r.ID))
		mu.Unlock()
	}
	var wg sync.WaitGroup
	dispatch(rt, &Request{ID: json.RawMessage(`"1"`), Method: "testSlow"}, out, &wg)
	done := make(chan struct{})
	go func() {
		dispatch(rt, &Request{ID: json.RawMessage(`"2"`), Method: "listProviders"}, out, &wg)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a fast request waited behind a slow one")
	}
	close(release)
	wg.Wait()
	if len(order) != 2 || order[0] != `"2"` || order[1] != `"1"` {
		t.Fatalf("order %v", order)
	}
	if !asyncMethods["sideQuestion"] {
		t.Fatal("sideQuestion must run off the request loop")
	}
}
