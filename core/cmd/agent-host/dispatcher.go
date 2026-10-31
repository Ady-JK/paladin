package main

import (
	"sync"
	"time"

	"github.com/Ady-JK/paladin/core/internal/runtime"
)

// runClass decides where a request runs. Control calls answer inline so
// they never wait behind slow work; everything else runs on a bounded
// worker pool.
type runClass int

const (
	// runInline answers on the request loop. Only quick, non-blocking
	// control calls belong here.
	runInline runClass = iota
	// runRead runs on the worker pool in no particular order.
	runRead
	// runSession runs on the worker pool one at a time per session, in
	// request order (keyed by the sessionId param).
	runSession
	// runGlobal runs on the worker pool one at a time, in request order,
	// for host-wide settings.
	runGlobal
	// runLong runs on a separate small pool (network or model calls) so it
	// cannot starve the main pool.
	runLong
)

var methodClass = map[string]runClass{
	"ping": runInline, "stopChat": runInline, "cancelTurn": runInline,
	"terminalWrite": runInline, "terminalResize": runInline, "terminalClose": runInline,
	"browserReply":   runInline,
	"answerApproval": runInline, "answerAsk": runInline, "cancelAsk": runInline,
	"turns": runInline, "resources": runInline, "getWorkspace": runInline,
	"fsHome": runInline, "resolveWorkspace": runInline,

	"listProviders": runRead, "fsList": runRead, "fileComplete": runRead, "gitInfo": runRead,
	"getPrompt": runRead, "listSkills": runRead, "listCommands": runRead, "memoryGet": runRead,
	"memoryProjects": runRead, "listSessions": runRead, "sessionHistory": runRead,
	"sessionTodos": runRead, "sessionUsage": runRead, "budgetGet": runRead, "thinkingGet": runRead,
	"overallUsage": runRead, "revertPreview": runRead, "sessionChanges": runRead, "sessionDiff": runRead, "worktreeStatus": runRead, "attach": runRead,
	"permissionsGet": runRead, "listAgents": runRead, "interruptAgent": runRead,
	"listSideChats": runRead, "loadSideChat": runRead, "hooksList": runRead, "mcpList": runRead,
	"mcpConfig": runRead, "mcpParse": runRead, "toolsList": runRead,

	"submitChat": runSession, "steerChat": runSession, "openSession": runSession,
	"terminalStart": runSession,
	"renameSession": runSession, "forkSession": runSession, "deleteSession": runSession,
	"revertSession": runSession, "unrevertSession": runSession, "worktreeMerge": runSession,
	"budgetSet": runSession, "thinkingSet": runSession, "memoryRefreshSnapshot": runSession,
	"deleteSideChat": runSession,

	"setDefaultProvider": runGlobal, "addProvider": runGlobal, "updateProvider": runGlobal,
	"setWorkspace": runGlobal, "setPrompt": runGlobal, "resetPrompt": runGlobal,
	"memorySet": runGlobal, "permissionsSet": runGlobal, "hooksSetConfig": runGlobal,
	"hooksTrust": runGlobal, "hooksToggle": runGlobal, "mcpSetConfig": runGlobal,
	"mcpSetEnabled": runGlobal, "mcpRestart": runGlobal, "mcpTrust": runGlobal,
	"mcpAdd": runGlobal, "toolsSet": runGlobal, "toolsAccept": runGlobal,

	"refreshProviders": runLong, "sideQuestion": runLong, "terminalRun": runLong,
}

// startsTurn marks the requests a later stopChat for the same session
// cancels if they have not started yet.
var startsTurn = map[string]bool{"submitChat": true, "steerChat": true}

const globalLane = "\x00global"

type dispatchLimits struct {
	Workers     int // main pool size
	LongWorkers int // long pool size
	MaxPending  int // queued + running pooled requests before "busy"
}

var defaultLimits = dispatchLimits{Workers: 8, LongWorkers: 4, MaxPending: 1024}

type job struct {
	req     *Request
	a       args
	lane    string // "" for unordered work
	session string
	stopGen uint64 // the session's stop count when the request arrived
}

type lane struct {
	queue []*job
	busy  bool
}

// dispatcher answers requests off the read loop with bounded
// concurrency. Requests on one lane keep their order; control calls
// answer inline; a stop cancels that session's turn requests still
// waiting behind it.
type dispatcher struct {
	rt     *runtime.AgentRuntime
	out    func(Response)
	limits dispatchLimits
	ready  chan *job
	long   chan *job

	mu       sync.Mutex
	closed   bool
	pending  int
	lanes    map[string]*lane
	stops    map[string]uint64
	starting map[string]*sync.Mutex // held while a turn request runs, and by stopChat

	workers sync.WaitGroup
}

func newDispatcher(rt *runtime.AgentRuntime, out func(Response), limits dispatchLimits) *dispatcher {
	d := &dispatcher{
		rt: rt, out: out, limits: limits,
		ready:    make(chan *job, limits.MaxPending),
		long:     make(chan *job, limits.MaxPending),
		lanes:    map[string]*lane{},
		stops:    map[string]uint64{},
		starting: map[string]*sync.Mutex{},
	}
	for range limits.Workers {
		d.workers.Add(1)
		go d.work(d.ready)
	}
	for range limits.LongWorkers {
		d.workers.Add(1)
		go d.work(d.long)
	}
	return d
}

// dispatch answers req, inline for control calls and through the pools
// otherwise. It never blocks on a handler that is not inline.
func (d *dispatcher) dispatch(req *Request) {
	a := parseArgs(req.Params)
	class := methodClass[req.Method]
	if _, known := methods[req.Method]; !known {
		class = runInline // answers "unknown method" at once
	}
	session := a.str("sessionId")
	d.mu.Lock()
	closed := d.closed
	d.mu.Unlock()
	if closed {
		d.out(errResponse(req.ID, "agent host is shutting down"))
		return
	}
	if class == runInline {
		if req.Method == "stopChat" && session != "" {
			d.stop(session, func() { d.answer(req, a) })
			return
		}
		d.answer(req, a)
		return
	}
	j := &job{req: req, a: a, session: session}
	switch class {
	case runSession:
		j.lane = session
		if j.lane == "" {
			j.lane = globalLane
		}
	case runGlobal:
		j.lane = globalLane
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		d.out(errResponse(req.ID, "agent host is shutting down"))
		return
	}
	if d.pending >= d.limits.MaxPending {
		d.out(errResponse(req.ID, "agent host is busy, try again"))
		return
	}
	d.pending++
	j.stopGen = d.stops[session]
	if j.lane == "" {
		d.enqueueLocked(j, class)
		return
	}
	l := d.lanes[j.lane]
	if l == nil {
		l = &lane{}
		d.lanes[j.lane] = l
	}
	if l.busy {
		l.queue = append(l.queue, j)
		return
	}
	l.busy = true
	d.enqueueLocked(j, class)
}

// enqueueLocked hands j to its pool. The channels hold MaxPending jobs,
// so this never blocks.
func (d *dispatcher) enqueueLocked(j *job, class runClass) {
	if class == runLong {
		d.long <- j
		return
	}
	d.ready <- j
}

// stop runs a stopChat: later-queued turn requests of the session see a
// new stop count, and one already starting finishes first so the stop
// reaches the turn it starts.
func (d *dispatcher) stop(session string, run func()) {
	d.mu.Lock()
	d.stops[session]++
	gate := d.startGateLocked(session)
	d.mu.Unlock()
	gate.Lock()
	defer gate.Unlock()
	run()
}

func (d *dispatcher) startGateLocked(session string) *sync.Mutex {
	gate := d.starting[session]
	if gate == nil {
		gate = &sync.Mutex{}
		d.starting[session] = gate
	}
	return gate
}

func (d *dispatcher) work(jobs <-chan *job) {
	defer d.workers.Done()
	for j := range jobs {
		d.run(j)
		d.finish(j)
	}
}

func (d *dispatcher) run(j *job) {
	d.mu.Lock()
	closed := d.closed
	stopped := startsTurn[j.req.Method] && d.stops[j.session] != j.stopGen
	var gate *sync.Mutex
	if startsTurn[j.req.Method] && j.session != "" {
		gate = d.startGateLocked(j.session)
	}
	d.mu.Unlock()
	switch {
	case closed:
		d.out(errResponse(j.req.ID, "agent host is shutting down"))
	case stopped:
		d.out(errResponse(j.req.ID, "stopped before it started"))
	case gate != nil:
		gate.Lock()
		// Re-check under the gate: a stop may have run while we waited.
		d.mu.Lock()
		stopped = d.stops[j.session] != j.stopGen
		d.mu.Unlock()
		if stopped {
			d.out(errResponse(j.req.ID, "stopped before it started"))
		} else {
			d.answer(j.req, j.a)
		}
		gate.Unlock()
	default:
		d.answer(j.req, j.a)
	}
}

// finish releases j's slot and starts the next request on its lane.
func (d *dispatcher) finish(j *job) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.pending--
	if j.lane == "" {
		return
	}
	l := d.lanes[j.lane]
	if len(l.queue) == 0 {
		delete(d.lanes, j.lane)
		return
	}
	next := l.queue[0]
	l.queue = l.queue[1:]
	if d.closed {
		// close already refused the queue; this lane is empty now.
		delete(d.lanes, j.lane)
		return
	}
	d.enqueueLocked(next, methodClass[next.req.Method])
}

func (d *dispatcher) answer(req *Request, a args) {
	var resp Response
	if err := guard("request "+req.Method, func() { resp = runHandler(d.rt, req, a) }); err != nil {
		resp = errResponse(req.ID, err.Error())
	}
	d.out(resp)
}

// close refuses queued and later requests, then waits up to grace for
// the running ones.
func (d *dispatcher) close(grace time.Duration) {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return
	}
	d.closed = true
	for name, l := range d.lanes {
		for _, j := range l.queue {
			d.out(errResponse(j.req.ID, "agent host is shutting down"))
			d.pending--
		}
		l.queue = nil
		if !l.busy {
			delete(d.lanes, name)
		}
	}
	close(d.ready)
	close(d.long)
	d.mu.Unlock()

	done := make(chan struct{})
	go func() { d.workers.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(grace):
		logf("WARN agent-host: requests still running after %s", grace)
	}
}
