package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Ady-JK/paladin/core/internal/runtime"
)

// responses collects dispatcher output by request id.
type responses struct {
	mu    sync.Mutex
	order []string
	byID  map[string]Response
	got   chan string
}

func newResponses() *responses {
	return &responses{byID: map[string]Response{}, got: make(chan string, 4096)}
}

func (r *responses) out(resp Response) {
	id := strings.Trim(string(resp.ID), `"`)
	r.mu.Lock()
	r.order = append(r.order, id)
	r.byID[id] = resp
	r.mu.Unlock()
	r.got <- id
}

// await waits for the response to id.
func (r *responses) await(t *testing.T, id string, within time.Duration) Response {
	t.Helper()
	deadline := time.After(within)
	for {
		r.mu.Lock()
		resp, ok := r.byID[id]
		r.mu.Unlock()
		if ok {
			return resp
		}
		select {
		case <-r.got:
		case <-deadline:
			t.Fatalf("no response to %s within %s", id, within)
		}
	}
}

func (r *responses) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.order)
}

// testMethod registers a method for one test, with its run class.
func testMethod(t *testing.T, name string, class runClass, fn handler) {
	t.Helper()
	methods[name] = fn
	methodClass[name] = class
	t.Cleanup(func() { delete(methods, name); delete(methodClass, name) })
}

func request(id, method string, params any) *Request {
	raw, _ := json.Marshal(params)
	return &Request{ID: json.RawMessage(`"` + id + `"`), Method: method, Params: raw}
}

func newTestDispatcher(t *testing.T, rt *runtime.AgentRuntime, workers, longWorkers, maxPending int) (*dispatcher, *responses) {
	t.Helper()
	got := newResponses()
	d := newDispatcher(rt, got.out, dispatchLimits{Workers: workers, LongWorkers: longWorkers, MaxPending: maxPending})
	t.Cleanup(func() { d.close(5 * time.Second) })
	return d, got
}

// Every method needs a declared run class: a new slow method must not
// silently run inline on the request loop.
func TestEveryMethodHasARunClass(t *testing.T) {
	for name := range methods {
		if _, ok := methodClass[name]; !ok {
			t.Errorf("method %q has no run class", name)
		}
	}
	for name := range methodClass {
		if _, ok := methods[name]; !ok {
			t.Errorf("run class for unknown method %q", name)
		}
	}
	for _, name := range []string{"stopChat", "cancelTurn", "answerApproval", "answerAsk", "cancelAsk", "ping"} {
		if methodClass[name] != runInline {
			t.Errorf("control method %q must run inline", name)
		}
	}
}

// A slow read, a slow global mutation and a slow session mutation must
// never hold up control calls.
func TestControlCallsAnswerWhileSlowWorkRuns(t *testing.T) {
	release := make(chan struct{})
	slow := func(*runtime.AgentRuntime, args) (any, error) { <-release; return "slow", nil }
	testMethod(t, "testSlowRead", runRead, slow)
	testMethod(t, "testSlowGlobal", runGlobal, slow)
	testMethod(t, "testSlowSession", runSession, slow)
	testMethod(t, "testSlowLong", runLong, slow)
	d, got := newTestDispatcher(t, testRuntime(t), 2, 1, 64)

	d.dispatch(request("r", "testSlowRead", nil))
	d.dispatch(request("g", "testSlowGlobal", nil))
	d.dispatch(request("s", "testSlowSession", map[string]string{"sessionId": "sess-a"}))
	d.dispatch(request("l", "testSlowLong", nil))
	start := time.Now()
	d.dispatch(request("stop", "stopChat", map[string]string{"sessionId": "sess-a"}))
	d.dispatch(request("approve", "answerApproval", map[string]string{"sessionId": "sess-a", "callId": "c1", "decision": "once"}))
	d.dispatch(request("ping", "ping", nil))
	for _, id := range []string{"stop", "approve", "ping"} {
		got.await(t, id, time.Second)
	}
	if took := time.Since(start); took > time.Second {
		t.Fatalf("control calls took %s behind slow work", took)
	}
	close(release)
	for _, id := range []string{"r", "g", "s", "l"} {
		if resp := got.await(t, id, 5*time.Second); !resp.OK || resp.Result != "slow" {
			t.Fatalf("%s = %+v", id, resp)
		}
	}
}

// Mutations of one session run one at a time, in request order, while
// other sessions proceed.
func TestSessionMutationsKeepTheirOrder(t *testing.T) {
	var (
		mu      sync.Mutex
		applied = map[string][]string{}
		running atomic.Int32
		overlap atomic.Bool
	)
	release := make(chan struct{})
	testMethod(t, "testMutate", runSession, func(_ *runtime.AgentRuntime, a args) (any, error) {
		if running.Add(1) > 1 && a.str("sessionId") == "sess-a" {
			overlap.Store(true)
		}
		defer running.Add(-1)
		if a.str("step") == "0" && a.str("sessionId") == "sess-a" {
			<-release
		}
		mu.Lock()
		applied[a.str("sessionId")] = append(applied[a.str("sessionId")], a.str("step"))
		mu.Unlock()
		return a.str("step"), nil
	})
	d, got := newTestDispatcher(t, testRuntime(t), 4, 1, 256)
	for i := range 20 {
		d.dispatch(request(fmt.Sprintf("a%d", i), "testMutate", map[string]string{"sessionId": "sess-a", "step": fmt.Sprint(i)}))
	}
	d.dispatch(request("b0", "testMutate", map[string]string{"sessionId": "sess-b", "step": "0"}))
	// sess-b is not stuck behind sess-a's slow first step.
	if resp := got.await(t, "b0", time.Second); !resp.OK {
		t.Fatalf("b0 = %+v", resp)
	}
	close(release)
	for i := range 20 {
		got.await(t, fmt.Sprintf("a%d", i), 5*time.Second)
	}
	mu.Lock()
	defer mu.Unlock()
	want := make([]string, 20)
	for i := range want {
		want[i] = fmt.Sprint(i)
	}
	if strings.Join(applied["sess-a"], ",") != strings.Join(want, ",") {
		t.Fatalf("sess-a applied out of order: %v", applied["sess-a"])
	}
	if overlap.Load() {
		t.Fatal("two mutations of one session ran at once")
	}
}

// A stop for a session refuses that session's turn requests still
// waiting behind a slow call: in request order the stop came after them.
func TestStopRefusesTurnRequestsStillWaiting(t *testing.T) {
	release := make(chan struct{})
	testMethod(t, "testSlowSession", runSession, func(*runtime.AgentRuntime, args) (any, error) { <-release; return "slow", nil })
	var submitted atomic.Int32
	realSubmit := methods["submitChat"]
	methods["submitChat"] = func(*runtime.AgentRuntime, args) (any, error) { submitted.Add(1); return "started", nil }
	t.Cleanup(func() { methods["submitChat"] = realSubmit })
	d, got := newTestDispatcher(t, testRuntime(t), 2, 1, 64)

	d.dispatch(request("slow", "testSlowSession", map[string]string{"sessionId": "sess-a"}))
	d.dispatch(request("submit", "submitChat", map[string]string{"sessionId": "sess-a", "message": "hi"}))
	d.dispatch(request("stop", "stopChat", map[string]string{"sessionId": "sess-a"}))
	d.dispatch(request("after", "submitChat", map[string]string{"sessionId": "sess-a", "message": "again"}))
	got.await(t, "stop", time.Second)
	close(release)
	if resp := got.await(t, "submit", 5*time.Second); resp.OK || !strings.Contains(*resp.Error, "stopped") {
		t.Fatalf("submit queued before the stop = %+v", resp)
	}
	if resp := got.await(t, "after", 5*time.Second); !resp.OK {
		t.Fatalf("submit sent after the stop = %+v", resp)
	}
	if n := submitted.Load(); n != 1 {
		t.Fatalf("submitChat ran %d times", n)
	}
}

// Work is bounded: no more than Workers handlers run at once, and
// requests past MaxPending are refused at once rather than queued.
func TestDispatchIsBounded(t *testing.T) {
	release := make(chan struct{})
	var running, peak atomic.Int32
	testMethod(t, "testBlock", runRead, func(*runtime.AgentRuntime, args) (any, error) {
		n := running.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		<-release
		running.Add(-1)
		return "ok", nil
	})
	d, got := newTestDispatcher(t, testRuntime(t), 3, 1, 10)
	for i := range 12 {
		d.dispatch(request(fmt.Sprint(i), "testBlock", nil))
	}
	for _, id := range []string{"10", "11"} {
		if resp := got.await(t, id, time.Second); resp.OK || !strings.Contains(*resp.Error, "busy") {
			t.Fatalf("request past the cap = %+v", resp)
		}
	}
	time.Sleep(50 * time.Millisecond)
	if p := peak.Load(); p != 3 {
		t.Fatalf("peak concurrency %d, want 3", p)
	}
	close(release)
	for i := range 10 {
		if resp := got.await(t, fmt.Sprint(i), 5*time.Second); !resp.OK {
			t.Fatalf("%d = %+v", i, resp)
		}
	}
	// The cap frees up as work finishes.
	d.dispatch(request("later", "testBlock", nil))
	if resp := got.await(t, "later", 5*time.Second); !resp.OK {
		t.Fatalf("later = %+v", resp)
	}
}

func TestDispatchAnswersErrorsAndPanicsByID(t *testing.T) {
	testMethod(t, "testPanic", runRead, func(*runtime.AgentRuntime, args) (any, error) { panic("boom") })
	d, got := newTestDispatcher(t, testRuntime(t), 2, 1, 16)
	d.dispatch(request("x", "noSuchMethod", nil))
	d.dispatch(request("p", "testPanic", nil))
	if resp := got.await(t, "x", time.Second); resp.OK || !strings.Contains(*resp.Error, "unknown method") {
		t.Fatalf("unknown = %+v", resp)
	}
	if resp := got.await(t, "p", time.Second); resp.OK || !strings.Contains(*resp.Error, "boom") {
		t.Fatalf("panic = %+v", resp)
	}
	d.dispatch(request("ok", "ping", nil))
	got.await(t, "ok", time.Second)
}

// close answers what is still queued with an error, waits (bounded) for
// running handlers, and refuses later requests.
func TestDispatcherCloseDrains(t *testing.T) {
	release := make(chan struct{})
	testMethod(t, "testBlock", runSession, func(*runtime.AgentRuntime, args) (any, error) { <-release; return "ok", nil })
	got := newResponses()
	d := newDispatcher(testRuntime(t), got.out, dispatchLimits{Workers: 1, LongWorkers: 1, MaxPending: 16})
	d.dispatch(request("running", "testBlock", map[string]string{"sessionId": "s"}))
	d.dispatch(request("queued", "testBlock", map[string]string{"sessionId": "s"}))
	time.Sleep(20 * time.Millisecond)
	closed := make(chan struct{})
	go func() { d.close(5 * time.Second); close(closed) }()
	if resp := got.await(t, "queued", time.Second); resp.OK || !strings.Contains(*resp.Error, "shutting down") {
		t.Fatalf("queued at close = %+v", resp)
	}
	close(release)
	<-closed
	if resp := got.await(t, "running", time.Second); !resp.OK {
		t.Fatalf("running at close = %+v", resp)
	}
	d.dispatch(request("late", "ping", nil))
	if resp := got.await(t, "late", time.Second); resp.OK {
		t.Fatalf("request after close = %+v", resp)
	}
	if got.count() != 3 {
		t.Fatalf("responses = %d", got.count())
	}
}
