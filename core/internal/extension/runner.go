package extension

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// A plugin gets one long-lived Node process (a warm runner) that speaks
// JSONL on stdio: one request per line in, one reply per line out. The
// pool here owns those processes: it starts them lazily, routes
// concurrent calls by id, reaps idle ones, and restarts after a crash.

const (
	// maxInflight caps concurrent calls on one runner; beyond it a call
	// fails fast instead of queueing inside the plugin.
	maxInflight = 16
	// shutdownGrace bounds a graceful stop before the process is killed.
	shutdownGrace = 3 * time.Second
	// reapInterval is how often idle runners are looked at.
	reapInterval = 30 * time.Second
	// stderrCap bounds the logging kept per runner for diagnostics.
	stderrCap = 10 << 10
	// dialTimeout bounds the first reply of a fresh runner (cold start:
	// spawn, import, activate).
	dialTimeout = 20 * time.Second
	// maxColdStarts caps runners starting at once, so ten plugins do not
	// spawn ten Nodes in the same millisecond.
	maxColdStarts = 4
	// coldQueueTimeout bounds how long a cold start waits for a slot.
	coldQueueTimeout = 30 * time.Second
)

// request is one line written to a runner.
type request struct {
	ID    string          `json:"id"`
	Op    string          `json:"op"`
	Tool  string          `json:"tool,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

// reply is one line read back.
type reply struct {
	ID     string          `json:"id"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// outcome is a routed reply (or the failure that replaced it).
type outcome struct {
	result json.RawMessage
	err    error
}

// waiter is one call waiting for its reply. counted marks the ones that
// occupy an inflight slot (tool calls, not ping/list).
type waiter struct {
	ch      chan outcome
	counted bool
}

// RunnerInfo is one warm runner as the tools UI shows it.
type RunnerInfo struct {
	Plugin string `json:"plugin"`
	Warm   bool   `json:"warm"`
	PID    int    `json:"pid,omitempty"`
	Node   string `json:"node,omitempty"`
	// Electron marks a runner hosted by the Electron binary.
	Electron bool `json:"electron,omitempty"`
	// LastUsed is when the runner last answered a call.
	LastUsed time.Time `json:"lastUsed,omitzero"`
	// Inflight is how many calls are waiting on it now.
	Inflight int `json:"inflight,omitempty"`
	// Generation counts restarts; it fences stale replies.
	Generation uint64 `json:"generation,omitempty"`
	// Stderr is the tail of the plugin's own logging, for diagnostics.
	Stderr string `json:"stderr,omitempty"`
}

// runnerProc is one plugin's warm Node process.
type runnerProc struct {
	plugin plugin
	node   nodeBin
	// gen fences ids: a reply from an older process cannot match.
	gen uint64

	mu       sync.Mutex
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	pending  map[string]waiter
	inflight int
	seq      uint64
	// exited is closed when the process is gone.
	exited   chan struct{}
	lastUsed time.Time
	starting bool
	// stderr keeps the tail of the plugin's own logging.
	stderr *ringBuffer
}

// Pool owns every warm runner, keyed by plugin name.
type Pool struct {
	mu      sync.Mutex
	runners map[string]*runnerProc
	// closed refuses new work once the app is shutting down.
	closed  chan struct{}
	cold    chan struct{}
	nextGen uint64
	now     func() time.Time
	// spawns counts runner starts and reaps counts idle kills, for tests
	// and the diagnostics log.
	spawns int
	reaps  int
	// scriptOnce writes the runner script for this pool on first use.
	scriptOnce sync.Once
	scriptDir  string
	scriptPath string
	scriptErr  error
}

// NewPool builds an empty pool.
func NewPool() *Pool {
	return &Pool{runners: map[string]*runnerProc{}, closed: make(chan struct{}),
		cold: make(chan struct{}, maxColdStarts), now: time.Now}
}

// Start reaps idle runners in the background until Close.
func (p *Pool) Start() {
	go func() {
		ticker := time.NewTicker(reapInterval)
		defer ticker.Stop()
		for {
			select {
			case <-p.closed:
				return
			case <-ticker.C:
				p.reapIdle()
			}
		}
	}()
}

// Close stops every runner: a shutdown line, a short grace, then a kill.
func (p *Pool) Close() {
	p.mu.Lock()
	select {
	case <-p.closed:
		p.mu.Unlock()
		return
	default:
	}
	close(p.closed)
	list := make([]*runnerProc, 0, len(p.runners))
	for _, r := range p.runners {
		list = append(list, r)
	}
	p.runners = map[string]*runnerProc{}
	p.mu.Unlock()
	var wg sync.WaitGroup
	for _, r := range list {
		wg.Add(1)
		go func(r *runnerProc) {
			defer wg.Done()
			r.stop(shutdownGrace)
		}(r)
	}
	wg.Wait()
	p.cleanRunnerScript()
}

// Drain stops every runner without closing the pool: a reload keeps
// serving new calls, on fresh processes.
func (p *Pool) Drain() {
	p.mu.Lock()
	list := make([]*runnerProc, 0, len(p.runners))
	for _, r := range p.runners {
		list = append(list, r)
	}
	p.runners = map[string]*runnerProc{}
	p.mu.Unlock()
	var wg sync.WaitGroup
	for _, r := range list {
		wg.Add(1)
		go func(r *runnerProc) {
			defer wg.Done()
			r.stop(shutdownGrace)
		}(r)
	}
	wg.Wait()
}

// Running reports the plugins with a live runner, for the tools UI.
func (p *Pool) Running() []RunnerInfo {
	p.mu.Lock()
	list := make([]*runnerProc, 0, len(p.runners))
	for _, r := range p.runners {
		list = append(list, r)
	}
	p.mu.Unlock()
	out := make([]RunnerInfo, 0, len(list))
	for _, r := range list {
		info := RunnerInfo{Plugin: r.plugin.name, Warm: true, Node: r.node.path, Electron: r.node.runAsNode}
		r.mu.Lock()
		if r.cmd != nil && r.cmd.Process != nil {
			info.PID = r.cmd.Process.Pid
		}
		info.LastUsed = r.lastUsed
		info.Inflight = r.inflight
		info.Generation = r.gen
		info.Stderr = r.stderr.tail(500)
		r.mu.Unlock()
		out = append(out, info)
	}
	return out
}

// Call runs one tool on a plugin, starting its runner when needed.
func (p *Pool) Call(ctx context.Context, pl plugin, tool string, input json.RawMessage, timeout time.Duration) (json.RawMessage, error) {
	return p.roundTrip(ctx, pl, request{Op: "call", Tool: tool, Input: input}, timeout, true)
}

// List asks a plugin for its tool declarations.
func (p *Pool) List(ctx context.Context, pl plugin, timeout time.Duration) (json.RawMessage, error) {
	return p.roundTrip(ctx, pl, request{Op: "list"}, timeout, false)
}

// Ping measures one warm round trip; it starts the runner too.
func (p *Pool) Ping(ctx context.Context, pl plugin, timeout time.Duration) error {
	_, err := p.roundTrip(ctx, pl, request{Op: "ping"}, timeout, false)
	return err
}

// roundTrip routes one request to the plugin's runner.
func (p *Pool) roundTrip(ctx context.Context, pl plugin, req request, timeout time.Duration, call bool) (json.RawMessage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	r, err := p.ensure(ctx, pl)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return r.value(ctx, req, call)
}

// ensure returns the plugin's live runner, starting one if needed.
// Concurrent callers end up sharing the one process.
func (p *Pool) ensure(ctx context.Context, pl plugin) (*runnerProc, error) {
	if err := p.acquireCold(ctx); err != nil {
		return nil, err
	}
	defer release(p.cold)
	p.mu.Lock()
	select {
	case <-p.closed:
		p.mu.Unlock()
		return nil, errors.New("extension pool is closed")
	default:
	}
	// Re-check under the lock: another caller may have started it while
	// this one waited for a cold-start slot.
	if r := p.runners[pl.name]; r != nil && r.alive() {
		p.mu.Unlock()
		return r, nil
	}
	gen := p.nextGen
	p.nextGen++
	p.spawns++
	r := &runnerProc{plugin: pl, gen: gen, pending: map[string]waiter{}, exited: make(chan struct{}),
		lastUsed: p.now(), starting: true, stderr: newRingBuffer(stderrCap)}
	p.runners[pl.name] = r
	p.mu.Unlock()

	if err := p.start(ctx, r); err != nil {
		p.forget(r)
		return nil, err
	}
	return r, nil
}

// acquireCold takes a cold-start slot, or gives up after the queue
// timeout so a spawn storm cannot pile up forever.
func (p *Pool) acquireCold(ctx context.Context) error {
	timer := time.NewTimer(coldQueueTimeout)
	defer timer.Stop()
	select {
	case p.cold <- struct{}{}:
		return nil
	case <-timer.C:
		return errors.New("extension cold start queue timed out")
	case <-p.closed:
		return errors.New("extension pool is closed")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func release(sem chan struct{}) { <-sem }

// forget drops a runner from the pool and fails its waiting calls.
func (p *Pool) forget(r *runnerProc) {
	p.mu.Lock()
	if p.runners[r.plugin.name] == r {
		delete(p.runners, r.plugin.name)
	}
	p.mu.Unlock()
	r.finishGone(errRunnerExited)
}

// start spawns the runner for r and waits for it to answer a ping, so a
// plugin that cannot import fails here instead of on the first call.
func (p *Pool) start(ctx context.Context, r *runnerProc) error {
	node, err := resolveNode(ctx, r.plugin)
	if err != nil {
		return fmt.Errorf("plugin %s: %w", r.plugin.name, err)
	}
	script, err := p.runnerScript()
	if err != nil {
		return fmt.Errorf("plugin %s: %w", r.plugin.name, err)
	}
	cmd := exec.Command(node.path, append(node.argv(), script, r.plugin.entry, r.plugin.dir)...)
	cmd.Dir = r.plugin.dir
	cmd.Env = runnerEnv(r.plugin)
	if node.runAsNode {
		cmd.Env = append(cmd.Env, "ELECTRON_RUN_AS_NODE=1")
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("plugin %s: %w", r.plugin.name, err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("plugin %s: %w", r.plugin.name, err)
	}
	cmd.Stderr = r.stderr
	cmd.SysProcAttr = runnerSysProcAttr()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("plugin %s: %w", r.plugin.name, err)
	}
	r.mu.Lock()
	r.node, r.cmd, r.stdin, r.starting = node, cmd, stdin, false
	r.mu.Unlock()
	go r.read(stdout)
	go func() { r.finish(cmd.Wait()) }()
	fmt.Fprintf(os.Stderr, "extension: plugin %s: runner started (pid %d, node %s)\n", r.plugin.name, cmd.Process.Pid, node.path)

	probeCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	if _, err := r.value(probeCtx, request{Op: "ping"}, false); err != nil {
		return fmt.Errorf("plugin %s: %w", r.plugin.name, err)
	}
	return nil
}

// reapIdle stops runners unused past their manifest's idle time. A
// runner with calls in flight is left alone.
func (p *Pool) reapIdle() {
	p.mu.Lock()
	var idle []*runnerProc
	for name, r := range p.runners {
		r.mu.Lock()
		limit := r.plugin.idle()
		unused := r.inflight == 0 && limit > 0 && p.now().Sub(r.lastUsed) > limit
		r.mu.Unlock()
		if unused {
			delete(p.runners, name)
			p.reaps++
			idle = append(idle, r)
		}
	}
	p.mu.Unlock()
	for _, r := range idle {
		fmt.Fprintf(os.Stderr, "extension: plugin %s: runner reaped after sitting idle\n", r.plugin.name)
		go r.stop(shutdownGrace)
	}
}

// alive reports whether the process is still there.
func (r *runnerProc) alive() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cmd != nil && !r.starting && r.running()
}

func (r *runnerProc) running() bool {
	select {
	case <-r.exited:
		return false
	default:
		return true
	}
}

// value writes one request and waits for its reply, bounded by ctx.
func (r *runnerProc) value(ctx context.Context, req request, call bool) (json.RawMessage, error) {
	id, ch, err := r.open(req, call)
	if err != nil {
		return nil, err
	}
	defer r.forgetPending(id)
	select {
	case out := <-ch:
		if out.err != nil {
			return nil, out.err
		}
		r.mu.Lock()
		r.lastUsed = time.Now()
		r.mu.Unlock()
		return out.result, nil
	case <-ctx.Done():
		// The runner keeps running: a slow call fails, the plugin does
		// not. Its reply, when it comes, finds no waiter and is dropped.
		return nil, fmt.Errorf("extension timeout: %w", ctx.Err())
	}
}

// open registers a pending reply and writes the request line.
func (r *runnerProc) open(req request, call bool) (string, chan outcome, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.running() || r.stdin == nil {
		return "", nil, errRunnerExited
	}
	if call && r.inflight >= maxInflight {
		return "", nil, errors.New("extension busy: the plugin already has the maximum calls running")
	}
	r.seq++
	req.ID = fmt.Sprintf("%d-%d", r.gen, r.seq)
	ch := make(chan outcome, 1)
	r.pending[req.ID] = waiter{ch: ch, counted: call}
	if call {
		r.inflight++
	}
	line, err := json.Marshal(req)
	if err != nil {
		delete(r.pending, req.ID)
		return "", nil, err
	}
	if _, err := r.stdin.Write(append(line, '\n')); err != nil {
		delete(r.pending, req.ID)
		if call {
			r.inflight--
		}
		return "", nil, fmt.Errorf("extension runner: %w", err)
	}
	return req.ID, ch, nil
}

// forgetPending unregisters a call whose reply the caller gave up on.
func (r *runnerProc) forgetPending(id string) {
	r.mu.Lock()
	if w, ok := r.pending[id]; ok {
		delete(r.pending, id)
		if w.counted {
			r.inflight--
		}
	}
	r.mu.Unlock()
}

// read routes reply lines to their waiting callers.
func (r *runnerProc) read(stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64<<10), maxOutput+1)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rep reply
		if err := json.Unmarshal(line, &rep); err != nil {
			fmt.Fprintf(os.Stderr, "extension %s: bad reply line: %v\n", r.plugin.name, err)
			continue
		}
		r.deliver(rep)
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		if errors.Is(err, bufio.ErrTooLong) {
			fmt.Fprintf(os.Stderr, "extension %s: reply exceeds %d bytes\n", r.plugin.name, maxOutput)
			r.fail(errors.New("extension output exceeds the reply cap"))
			return
		}
		fmt.Fprintf(os.Stderr, "extension %s: read runner output: %v\n", r.plugin.name, err)
	}
}

// deliver hands one reply to its caller; unknown ids are stale (an older
// generation) and ignored.
func (r *runnerProc) deliver(rep reply) {
	r.mu.Lock()
	w, ok := r.pending[rep.ID]
	if ok {
		delete(r.pending, rep.ID)
		if w.counted {
			r.inflight--
		}
	}
	r.mu.Unlock()
	if !ok {
		return
	}
	if rep.OK {
		w.ch <- outcome{result: rep.Result}
		return
	}
	msg := strings.TrimSpace(rep.Error)
	if msg == "" {
		msg = "the plugin reported a failure"
	}
	w.ch <- outcome{err: errors.New(msg)}
}

// finish marks the process gone and fails everything waiting on it.
func (r *runnerProc) finish(waitErr error) {
	reason := errRunnerExited
	if waitErr != nil {
		reason = fmt.Errorf("extension runner exited: %w", waitErr)
		if tail := strings.TrimSpace(r.stderr.tail(2 << 10)); tail != "" {
			fmt.Fprintf(os.Stderr, "extension %s: %v\n%s\n", r.plugin.name, waitErr, tail)
		}
	}
	r.finishGone(reason)
}

// fail drops the process after a protocol violation (an oversized reply).
func (r *runnerProc) fail(reason error) {
	r.mu.Lock()
	cmd := r.cmd
	r.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	r.finishGone(reason)
}

// finishGone delivers reason to every pending call, once.
func (r *runnerProc) finishGone(reason error) {
	r.mu.Lock()
	select {
	case <-r.exited:
		r.mu.Unlock()
		return
	default:
	}
	close(r.exited)
	pending := r.pending
	r.pending = map[string]waiter{}
	r.inflight = 0
	r.mu.Unlock()
	if len(pending) > 0 {
		fmt.Fprintf(os.Stderr, "extension %s: %v, %d call(s) dropped\n", r.plugin.name, reason, len(pending))
	}
	for _, w := range pending {
		w.ch <- outcome{err: reason}
	}
}

var errRunnerExited = errors.New("extension runner exited")

// stop asks the runner to shut down, then kills it after grace.
func (r *runnerProc) stop(grace time.Duration) {
	r.mu.Lock()
	cmd, stdin, running := r.cmd, r.stdin, r.cmd != nil && r.running()
	r.mu.Unlock()
	if !running {
		r.finishGone(errRunnerExited)
		return
	}
	if stdin != nil {
		line, _ := json.Marshal(request{ID: "shutdown", Op: "shutdown"})
		_, _ = stdin.Write(append(line, '\n'))
	}
	select {
	case <-r.exited:
		return
	case <-time.After(grace):
	}
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	select {
	case <-r.exited:
	case <-time.After(grace):
	}
	r.finishGone(errRunnerExited)
}

// runnerEnv is the child's environment: the user's own plus the
// manifest's additions (PATH stays the user's, which loadManifest
// enforces).
func runnerEnv(pl plugin) []string {
	env := os.Environ()
	for k, v := range pl.Env {
		env = append(env, k+"="+v)
	}
	return env
}
