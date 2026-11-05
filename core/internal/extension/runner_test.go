package extension

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// poolFor starts a pool wired to one plugin directory and closes it at
// the end of the test.
func poolFor(t *testing.T, files map[string]string) (*Pool, plugin) {
	t.Helper()
	withNode(t)
	dataDir := t.TempDir()
	writePlugin(t, dataDir, "probe", files)
	pl, err := loadManifest(filepath.Join(dataDir, "config", "extensions"), "probe")
	if err != nil {
		t.Fatal(err)
	}
	pool := NewPool()
	pool.Start()
	t.Cleanup(pool.Close)
	return pool, pl
}

// callToolNamed runs one tool on a plugin through the pool.
func callPlugin(t *testing.T, pool *Pool, pl plugin, tool string, args any) (any, error) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"args": args, "context": map[string]string{"workspace": "/tmp"}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := pool.Call(context.Background(), pl, tool, raw, 10*time.Second)
	if err != nil {
		return nil, err
	}
	// A tool's result is a JSON string holding the plugin's own JSON (or
	// plain text), which is what the tool sees.
	var text string
	if json.Unmarshal(out, &text) == nil {
		return decodeOutput(text), nil
	}
	return string(out), nil
}

// A throwing call answers that call and leaves the runner alive.
func TestRunnerIsolatesCallErrors(t *testing.T) {
	pool, pl := poolFor(t, map[string]string{"index.ts": `
export default { tools: [{
  name: "boom", description: "Throw", parameters: { type: "object" },
  execute() { throw new Error("kaboom"); },
}, {
  name: "ok", description: "Fine", parameters: { type: "object" },
  execute() { return "fine"; },
}] };`})
	if _, err := callPlugin(t, pool, pl, "boom", map[string]any{}); err == nil ||
		!strings.Contains(err.Error(), "kaboom") {
		t.Fatalf("error %v", err)
	}
	out, err := callPlugin(t, pool, pl, "ok", map[string]any{})
	if err != nil || out != "fine" {
		t.Fatalf("the runner should have survived: %v %v", out, err)
	}
	if len(pool.Running()) != 1 {
		t.Fatalf("runners %+v", pool.Running())
	}
}

// A plugin that cannot import fails the load with its own message.
func TestRunnerImportFailure(t *testing.T) {
	pool, pl := poolFor(t, map[string]string{"index.ts": `throw new Error("no import for you");`})
	_, err := callPlugin(t, pool, pl, "anything", map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "plugin probe") {
		t.Fatalf("error %v", err)
	}
	if len(pool.Running()) != 0 {
		t.Fatalf("a failed plugin stayed in the pool: %+v", pool.Running())
	}
}

// Calls run concurrently on one process, and replies reach the right
// caller.
func TestRunnerMultiplexesConcurrentCalls(t *testing.T) {
	pool, pl := poolFor(t, map[string]string{"index.ts": `
export default { tools: [{
  name: "slow", description: "Echo after a delay", parameters: { type: "object" },
  execute(args) { return new Promise((done) => setTimeout(() => done(args.n), args.n)); },
}] };`})
	var wg sync.WaitGroup
	errs := make([]error, 6)
	results := make([]any, 6)
	for i := range 6 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = callPlugin(t, pool, pl, "slow", map[string]any{"n": 10 + i})
		}(i)
	}
	wg.Wait()
	for i := range 6 {
		if errs[i] != nil || results[i] != float64(10+i) {
			t.Fatalf("call %d: %v %v", i, results[i], errs[i])
		}
	}
}

// The inflight cap fails fast instead of queueing inside the plugin.
func TestRunnerInflightCap(t *testing.T) {
	pool, pl := poolFor(t, map[string]string{"index.ts": `
export default { tools: [{
  name: "wait", description: "Never answers", parameters: { type: "object" },
  execute() { return new Promise(() => {}); },
}] };`})
	r, err := pool.ensure(context.Background(), pl)
	if err != nil {
		t.Fatal(err)
	}
	// Fill the cap directly, then one more call must be refused.
	var open []string
	for range maxInflight {
		id, _, err := r.open(request{Op: "call", Tool: "wait"}, true)
		if err != nil {
			t.Fatalf("opening within the cap: %v", err)
		}
		open = append(open, id)
	}
	if _, _, err := r.open(request{Op: "call", Tool: "wait"}, true); err == nil ||
		!strings.Contains(err.Error(), "extension busy") {
		t.Fatalf("17th call accepted: %v", err)
	}
	for _, id := range open {
		r.forgetPending(id)
	}
}

// A call that outruns its timeout fails, but the runner survives and the
// next call works.
func TestRunnerTimeoutKeepsRunner(t *testing.T) {
	pool, pl := poolFor(t, map[string]string{"index.ts": `
export default { tools: [{
  name: "nap", description: "Sleep", parameters: { type: "object" },
  execute(args) { return new Promise((done) => setTimeout(() => done("late"), args.ms || 0)); },
}] };`})
	raw, _ := json.Marshal(map[string]any{"args": map[string]any{"ms": 1500}, "context": map[string]string{}})
	start := time.Now()
	if _, err := pool.Call(context.Background(), pl, "nap", raw, 150*time.Millisecond); err == nil ||
		!strings.Contains(err.Error(), "extension timeout") {
		t.Fatalf("error %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("the timeout did not bound the call")
	}
	out, err := callPlugin(t, pool, pl, "nap", map[string]any{"ms": 0})
	if err != nil || out != "late" {
		t.Fatalf("the runner should have survived: %v %v", out, err)
	}
}

// nopWriteCloser stands in for a runner's stdin in unit tests.
type nopWriteCloser struct{}

func (nopWriteCloser) Write(p []byte) (int, error) { return len(p), nil }
func (nopWriteCloser) Close() error                { return nil }

// Killing the runner fails in-flight calls and the next call cold-starts
// a fresh process.
func TestRunnerCrashRestarts(t *testing.T) {
	pool, pl := poolFor(t, map[string]string{"index.ts": `
export default { tools: [{
  name: "pid", description: "Report the process", parameters: { type: "object" },
  execute() { return { pid: process.pid }; },
}] };`})
	first, err := callPlugin(t, pool, pl, "pid", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	pid := int(first.(map[string]any)["pid"].(float64))
	if err := killProcess(pid); err != nil {
		t.Fatal(err)
	}
	// The next call either reuses the pool or cold-starts; either way it
	// must answer from a new process.
	var second any
	for range 50 {
		second, err = callPlugin(t, pool, pl, "pid", map[string]any{})
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("restart failed: %v", err)
	}
	if int(second.(map[string]any)["pid"].(float64)) == pid {
		t.Fatal("the call did not reach a fresh process")
	}
}

// An idle runner is reaped, and the next call starts a new one.
func TestPoolReapsIdleRunner(t *testing.T) {
	pool, pl := poolFor(t, map[string]string{
		"paladin-plugin.json": `{"idleMs":1000}`,
		"index.ts": `export default { tools: [{
  name: "pid", description: "Report the process", parameters: { type: "object" },
  execute() { return { pid: process.pid }; },
}] };`,
	})
	first, err := callPlugin(t, pool, pl, "pid", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if len(pool.Running()) != 1 {
		t.Fatal("no warm runner")
	}
	// Move the clock forward instead of waiting a second.
	pool.mu.Lock()
	pool.now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	pool.mu.Unlock()
	pool.reapIdle()
	if pool.reaps != 1 || len(pool.Running()) != 0 {
		t.Fatalf("reaps %d running %+v", pool.reaps, pool.Running())
	}
	second, err := callPlugin(t, pool, pl, "pid", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if int(second.(map[string]any)["pid"].(float64)) == int(first.(map[string]any)["pid"].(float64)) {
		t.Fatal("the reaped runner was somehow reused")
	}
}

// Closing the pool stops the process and refuses new work.
func TestPoolCloseStopsRunners(t *testing.T) {
	pool, pl := poolFor(t, map[string]string{"index.ts": `export default { tools: [{
  name: "pid", description: "Report the process", parameters: { type: "object" },
  execute() { return process.pid; },
}] };`})
	first, err := callPlugin(t, pool, pl, "pid", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	pid := int(first.(float64))
	start := time.Now()
	pool.Close()
	if time.Since(start) > shutdownGrace*2 {
		t.Fatal("close should not wait longer than the grace")
	}
	if processAlive(t, pid) {
		t.Fatalf("runner %d outlived the pool", pid)
	}
	if _, err := callPlugin(t, pool, pl, "pid", map[string]any{}); err == nil {
		t.Fatal("a closed pool still served a call")
	}
}

// A reply from an older generation is ignored: ids are fenced by the
// runner's generation counter.
func TestRunnerGenerationFencesReplies(t *testing.T) {
	r := &runnerProc{plugin: plugin{name: "p", Manifest: Manifest{}}, gen: 7, pending: map[string]waiter{},
		exited: make(chan struct{}), stderr: newRingBuffer(64), stdin: nopWriteCloser{}}
	id, ch, err := r.open(request{Op: "ping"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(id, "7-") {
		t.Fatalf("id %q is not generation-prefixed", id)
	}
	r.deliver(reply{ID: "6-1", OK: true, Result: json.RawMessage(`"stale"`)})
	select {
	case out := <-ch:
		t.Fatalf("a stale reply was delivered: %+v", out)
	default:
	}
	r.deliver(reply{ID: id, OK: true, Result: json.RawMessage(`"fresh"`)})
	if out := <-ch; string(out.result) != `"fresh"` {
		t.Fatalf("out %+v", out)
	}
}

// An oversized reply drops the runner instead of buffering it.
func TestRunnerReplyCap(t *testing.T) {
	pool, pl := poolFor(t, map[string]string{"index.ts": `export default { tools: [{
  name: "huge", description: "Return too much", parameters: { type: "object" },
  execute() { return "x".repeat(2 * 1024 * 1024); },
}] };`})
	_, err := callPlugin(t, pool, pl, "huge", map[string]any{})
	if err == nil {
		t.Fatal("an oversized reply was accepted")
	}
	if !strings.Contains(err.Error(), "reply exceeds") && !strings.Contains(err.Error(), "output exceeds") {
		t.Fatalf("error %v", err)
	}
}

// stderr from the plugin is kept for diagnostics and never mixed into
// the protocol.
func TestRunnerStderrKept(t *testing.T) {
	pool, pl := poolFor(t, map[string]string{"index.ts": `export default { tools: [{
  name: "log", description: "Log then answer", parameters: { type: "object" },
  execute() { console.log("hello from the plugin"); return "ok"; },
}] };`})
	out, err := callPlugin(t, pool, pl, "log", map[string]any{})
	if err != nil || out != "ok" {
		t.Fatalf("plugin logging broke the protocol: %v %v", out, err)
	}
	runners := pool.Running()
	if len(runners) != 1 || !strings.Contains(runners[0].Stderr, "hello from the plugin") {
		t.Fatalf("stderr %+v", runners)
	}
}

// onActivate runs at start and onDeactivate at a graceful stop.
func TestRunnerLifecycleHooks(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "life.txt")
	pool, pl := poolFor(t, map[string]string{"index.ts": fmt.Sprintf(`import { appendFileSync } from 'node:fs';
export default {
  onActivate(ctx) { appendFileSync(%q, 'activate:' + ctx.pluginDir.split('/').pop() + '\n'); },
  onDeactivate() { appendFileSync(%q, 'deactivate\n'); },
  tools: [{ name: "noop", description: "Do nothing", parameters: { type: "object" }, execute() { return "noop"; } }],
};`, marker, marker)})
	if _, err := callPlugin(t, pool, pl, "noop", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	pool.Close()
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "activate:probe\ndeactivate\n" {
		t.Fatalf("lifecycle file %q", data)
	}
}

// Concurrent first calls share one process: one spawn, one runner.
func TestConcurrentColdStartsShareOneProcess(t *testing.T) {
	pool, pl := poolFor(t, map[string]string{"index.ts": `
export default { tools: [{
  name: "pid", description: "Report the process", parameters: { type: "object" },
  execute() { return process.pid; },
}] };`})
	var wg sync.WaitGroup
	pids := make([]int, 8)
	for i := range 8 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			out, err := callPlugin(t, pool, pl, "pid", map[string]any{})
			if err != nil {
				t.Errorf("call %d: %v", i, err)
				return
			}
			pids[i] = int(out.(float64))
		}(i)
	}
	wg.Wait()
	if pool.spawns != 1 {
		t.Fatalf("%d spawns for one plugin, want 1", pool.spawns)
	}
	for _, pid := range pids {
		if pid != pids[0] {
			t.Fatalf("calls reached different processes: %v", pids)
		}
	}
}

// A reload never cuts a call off mid-flight: the running call finishes on
// the old process, and the next call gets a fresh one.
func TestReloadLetsACallFinish(t *testing.T) {
	pool, pl := poolFor(t, map[string]string{"index.ts": `
export default { tools: [{
  name: "slow", description: "Answer after a pause", parameters: { type: "object" },
  execute() { return new Promise((done) => setTimeout(() => done(process.pid), 600)); },
}] };`})
	started := make(chan struct{})
	result := make(chan outcomePair, 1)
	go func() {
		close(started)
		out, err := callPlugin(t, pool, pl, "slow", map[string]any{})
		result <- outcomePair{out: out, err: err}
	}()
	<-started
	// Let the call reach the runner, then reload under it.
	for range 100 {
		if running := pool.Running(); len(running) == 1 && running[0].Inflight == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	pool.Drain()
	if got := <-result; got.err != nil || got.out == nil {
		t.Fatalf("the reload cut the call off: %v %v", got.out, got.err)
	}
	if len(pool.Running()) != 0 {
		t.Fatalf("a drained pool kept a runner: %+v", pool.Running())
	}
	// The next call works on a new process, with a new generation.
	out, err := callPlugin(t, pool, pl, "slow", map[string]any{})
	if err != nil || out == nil {
		t.Fatalf("after reload: %v %v", out, err)
	}
	if len(pool.Running()) != 1 || pool.Running()[0].Generation == 0 {
		t.Fatalf("generation not bumped: %+v", pool.Running())
	}
}

type outcomePair struct {
	out any
	err error
}

// A cold start slower than the caller's deadline fails that call but not
// the plugin: the process stays pooled, so the next call reuses it
// instead of spawning a second Node.
func TestSlowColdStartKeepsTheRunner(t *testing.T) {
	pool, pl := poolFor(t, map[string]string{"index.ts": `
await new Promise((done) => setTimeout(done, 900));
export default { tools: [{
  name: "pid", description: "Report the process", parameters: { type: "object" },
  execute() { return process.pid; },
}] };`})
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	raw := json.RawMessage(`{"args":{},"context":{"workspace":"/tmp"}}`)
	if _, err := pool.Call(ctx, pl, "pid", raw, 10*time.Second); err == nil {
		t.Fatal("the cold start was slower than the call deadline, so the call should fail")
	}
	if pool.spawns != 1 {
		t.Fatalf("%d spawns, want 1", pool.spawns)
	}
	if len(pool.Running()) != 1 {
		t.Fatalf("the process was dropped: %+v", pool.Running())
	}
	// The import is still running; this call waits for it on the same pid.
	out, err := callPlugin(t, pool, pl, "pid", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out.(float64); !ok {
		t.Fatalf("call after the slow start returned %T %v", out, out)
	}
	if pool.spawns != 1 {
		t.Fatalf("%d spawns after reuse, want 1", pool.spawns)
	}
}
