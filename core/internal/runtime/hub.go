package runtime

import (
	"context"
	"fmt"
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Ady-JK/paladin/core/internal/agent"
	"github.com/Ady-JK/paladin/core/internal/hooks"
	"github.com/Ady-JK/paladin/core/internal/permission"
)

// Turn states.
const (
	TurnQueued  = "queued"
	TurnRunning = "running"
	TurnDone    = "done"
)

// RunFunc runs one turn. It must not close tx; the hub does. gate wraps
// the end-of-turn transcript write (agent.Deps.PersistGate).
type RunFunc func(ctx context.Context, req agent.Request, gate func(func()), tx chan<- agent.ChatChunk, done <-chan struct{}) (agent.ChatOutcome, error)

// TurnInfo describes one submitted turn.
type TurnInfo struct {
	TurnID    string `json:"turnId"`
	SessionID string `json:"sessionId"`
	Cwd       string `json:"cwd"`
	Message   string `json:"message"`
	// Display is the client's rendering of the message (before @path
	// expansion); it rides along so a re-attaching UI can redraw it.
	Display string `json:"display,omitempty"`
	Skill   string `json:"skill,omitempty"`
	Command string `json:"command,omitempty"`
	Mode    string `json:"mode,omitempty"`
	Compact bool   `json:"compact,omitempty"`
	Resume  bool   `json:"resume,omitempty"`
	// Permissions is the turn's approval preset as submitted.
	Permissions string `json:"permissions,omitempty"`
	State       string `json:"state"`
	// Persisted means the transcript already holds this turn.
	Persisted bool   `json:"persisted,omitempty"`
	Reply     string `json:"reply,omitempty"`
	Stopped   bool   `json:"stopped,omitempty"`
	Cancelled bool   `json:"cancelled,omitempty"`
	// AwaitingApproval is set while a tool call waits on the user.
	AwaitingApproval bool   `json:"awaitingApproval,omitempty"`
	Error            string `json:"error,omitempty"`
	QueuedAt         int64  `json:"queuedAt"`
	StartedAt        int64  `json:"startedAt,omitempty"`
	EndedAt          int64  `json:"endedAt,omitempty"`
}

// TurnEvent is one hub broadcast: a streamed chat/* chunk or a
// turn/{queued,start,done} lifecycle change. Seq is global and strictly
// increasing so a re-attaching client can drop what its snapshot holds.
type TurnEvent struct {
	Event      string                  `json:"event"`
	Seq        uint64                  `json:"seq"`
	SessionID  string                  `json:"sessionId"`
	TurnID     string                  `json:"turnId"`
	Delta      string                  `json:"delta,omitempty"`
	Tool       *agent.ToolActivity     `json:"tool,omitempty"`
	Retry      *agent.RetryStatus      `json:"retry,omitempty"`
	Compaction *agent.CompactionStatus `json:"compaction,omitempty"`
	Approval   *permission.Approval    `json:"approval,omitempty"`
	Steer      *agent.SteerMessage     `json:"steer,omitempty"`
	Budget     *agent.BudgetNotice     `json:"budget,omitempty"`
	Hook       *hooks.Note             `json:"hook,omitempty"`
	Turn       *TurnInfo               `json:"turn,omitempty"`
}

// SubmitOpts carries client-side extras that never reach the agent.
type SubmitOpts struct {
	Display string
}

// Attachment is everything a client needs to show a session: its
// history, the live turn's events so far, and what is queued behind it.
type Attachment struct {
	History any         `json:"history"`
	Turn    *TurnInfo   `json:"turn"`
	Events  []TurnEvent `json:"events"`
	Queued  []TurnInfo  `json:"queued"`
	Seq     uint64      `json:"seq"`
}

type hubTurn struct {
	info     TurnInfo
	req      agent.Request
	cancel   context.CancelFunc
	log      []TurnEvent
	toolAt   map[string]int
	waiting  map[string]bool
	reply    strings.Builder
	errMsg   string
	result   chan TurnInfo
	finished chan struct{}
	// steer holds messages sent mid-turn that the agent has not taken.
	steer []agent.SteerMessage
	// halted marks a user stop (or shutdown), as opposed to the turn
	// ending on its own.
	halted bool
}

// halt stops the turn on the user's behalf. Callers hold the hub lock.
func (t *hubTurn) halt() {
	t.halted = true
	t.cancel()
}

// Hub owns every turn: it runs different sessions concurrently (up to
// a cap), queues follow-ups per session in FIFO order, and keeps each
// live turn's events so clients can detach and re-attach at will.
type Hub struct {
	run   RunFunc
	limit func() int

	mu      sync.Mutex
	sink    func(TurnEvent)
	seq     uint64
	nextID  atomic.Uint64
	active  map[string]*hubTurn
	queue   []*hubTurn
	closed  bool
	running sync.WaitGroup
}

// NewHub builds a hub; limit is read on every scheduling pass so a
// settings change applies to the next turn that would start.
func NewHub(run RunFunc, limit func() int) *Hub {
	return &Hub{run: run, limit: limit, active: map[string]*hubTurn{}, sink: func(TurnEvent) {}}
}

// SetSink sets the broadcast target. It is called with the hub lock
// held, so events arrive in seq order; it must not call back into the hub.
func (h *Hub) SetSink(sink func(TurnEvent)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if sink == nil {
		sink = func(TurnEvent) {}
	}
	h.sink = sink
}

// Submit queues a turn and starts it at once when its session is idle
// and a slot is free. The channel receives the final TurnInfo.
func (h *Hub) Submit(req agent.Request, opts SubmitOpts) (TurnInfo, <-chan TurnInfo, error) {
	if strings.TrimSpace(req.SessionID) == "" {
		return TurnInfo{}, nil, fmt.Errorf("sessionId is required: create one with openSession first")
	}
	if strings.TrimSpace(req.Message) == "" && !req.Resume && !req.Compact && derefString(req.Command) == "" {
		return TurnInfo{}, nil, fmt.Errorf("message is required")
	}
	if _, err := permission.ParsePreset(req.Permissions); err != nil {
		return TurnInfo{}, nil, err
	}
	t := h.newTurn(req, opts)
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return TurnInfo{}, nil, fmt.Errorf("agent host is shutting down")
	}
	h.queue = append(h.queue, t)
	h.pumpLocked()
	if t.info.State == TurnQueued {
		h.publishLocked(t, TurnEvent{Event: "turn/queued", Turn: infoPtr(t.info)})
	}
	return t.info, t.result, nil
}

func (h *Hub) newTurn(req agent.Request, opts SubmitOpts) *hubTurn {
	return &hubTurn{
		req:      req,
		toolAt:   map[string]int{},
		waiting:  map[string]bool{},
		result:   make(chan TurnInfo, 1),
		finished: make(chan struct{}),
		info: TurnInfo{
			TurnID:      fmt.Sprintf("t%d-%d", time.Now().UnixMilli(), h.nextID.Add(1)),
			SessionID:   req.SessionID,
			Cwd:         req.Cwd,
			Message:     req.Message,
			Display:     opts.Display,
			Skill:       derefString(req.Skill),
			Command:     derefString(req.Command),
			Mode:        req.Mode,
			Permissions: req.Permissions,
			Compact:     req.Compact,
			Resume:      req.Resume,
			State:       TurnQueued,
			QueuedAt:    time.Now().UnixMilli(),
		},
	}
}

// Steer sends a message into the session's running turn; the agent
// adds it before its next model call. A message the turn never takes
// (it was already wrapping up) becomes the session's next turn, unless
// the user stops the turn. It fails when nothing is running, so the
// client can submit a normal turn instead.
func (h *Hub) Steer(sessionID, message, display string) (TurnInfo, error) {
	if strings.TrimSpace(sessionID) == "" {
		return TurnInfo{}, fmt.Errorf("sessionId is required")
	}
	if strings.TrimSpace(message) == "" {
		return TurnInfo{}, fmt.Errorf("message is required")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	t := h.active[sessionID]
	if t == nil || t.halted {
		return TurnInfo{}, fmt.Errorf("no turn is running in this session")
	}
	t.steer = append(t.steer, agent.SteerMessage{Message: message, Display: display})
	return t.info, nil
}

// takeSteer hands the turn's pending steer messages to its agent.
func (h *Hub) takeSteer(t *hubTurn) []agent.SteerMessage {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := t.steer
	t.steer = nil
	return out
}

// queueLeftoverSteerLocked turns steer messages the agent never took
// into a follow-up turn with the same settings, ahead of anything the
// session queued later.
func (h *Hub) queueLeftoverSteerLocked(t *hubTurn) {
	if len(t.steer) == 0 || t.halted || h.closed {
		t.steer = nil
		return
	}
	var msgs, shown []string
	for _, m := range t.steer {
		msgs = append(msgs, m.Message)
		d := m.Display
		if strings.TrimSpace(d) == "" {
			d = m.Message
		}
		shown = append(shown, d)
	}
	t.steer = nil
	req := t.req
	req.Message = strings.Join(msgs, "\n\n")
	req.Display = ""
	req.Resume, req.Compact = false, false
	req.Skill, req.Command = nil, nil
	req.Steer = nil
	display := strings.Join(shown, "\n\n")
	if display == req.Message {
		display = ""
	}
	next := h.newTurn(req, SubmitOpts{Display: display})
	at := len(h.queue)
	for i, q := range h.queue {
		if q.info.SessionID == t.info.SessionID {
			at = i
			break
		}
	}
	h.queue = append(h.queue, nil)
	copy(h.queue[at+1:], h.queue[at:])
	h.queue[at] = next
	h.publishLocked(next, TurnEvent{Event: "turn/queued", Turn: infoPtr(next.info)})
}

// pumpLocked starts every queued turn whose session is idle while
// slots remain, in submission order.
func (h *Hub) pumpLocked() {
	if h.closed {
		return
	}
	limit := 1
	if h.limit != nil {
		if n := h.limit(); n > 0 {
			limit = n
		}
	}
	kept := h.queue[:0]
	for _, t := range h.queue {
		if len(h.active) >= limit || h.active[t.info.SessionID] != nil {
			kept = append(kept, t)
			continue
		}
		h.startLocked(t)
	}
	for i := len(kept); i < len(h.queue); i++ {
		h.queue[i] = nil
	}
	h.queue = kept
}

func (h *Hub) startLocked(t *hubTurn) {
	ctx, cancel := context.WithCancel(context.Background())
	t.cancel = cancel
	t.req.Steer = func() []agent.SteerMessage { return h.takeSteer(t) }
	t.info.State = TurnRunning
	t.info.StartedAt = time.Now().UnixMilli()
	h.active[t.info.SessionID] = t
	h.publishLocked(t, TurnEvent{Event: "turn/start", Turn: infoPtr(t.info)})
	h.running.Add(1)
	go h.drive(ctx, t)
}

// drive runs one turn, forwarding its chunks until the run returns.
func (h *Hub) drive(ctx context.Context, t *hubTurn) {
	defer h.running.Done()
	tx := make(chan agent.ChatChunk, 128)
	done := make(chan struct{})
	forwarded := make(chan struct{})
	go func() {
		defer close(forwarded)
		for chunk := range tx {
			h.forward(t, chunk)
		}
	}()
	gate := func(persist func()) {
		h.mu.Lock()
		defer h.mu.Unlock()
		persist()
		t.info.Persisted = true
	}
	var (
		out agent.ChatOutcome
		err error
	)
	func() {
		defer func() {
			if p := recover(); p != nil {
				fmt.Fprintf(os.Stderr, "turn panicked: %v\n%s", p, debug.Stack())
				err = fmt.Errorf("internal error: %v", p)
			}
		}()
		out, err = h.run(ctx, t.req, gate, tx, done)
	}()
	close(tx)
	<-forwarded
	close(done)
	t.cancel()
	h.finish(t, out, err)
}

func (h *Hub) forward(t *hubTurn, chunk agent.ChatChunk) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if chunk.Error != nil && t.errMsg == "" {
		t.errMsg = chunk.Error.Message
	}
	if chunk.Stopped {
		t.info.Stopped = true
	}
	if chunk.Delta != "" {
		t.reply.WriteString(chunk.Delta)
		h.publishLocked(t, TurnEvent{Event: "chat/delta", Delta: chunk.Delta})
	}
	if chunk.Retry != nil {
		h.publishLocked(t, TurnEvent{Event: "chat/retry", Retry: chunk.Retry})
	}
	if chunk.Compaction != nil {
		h.publishLocked(t, TurnEvent{Event: "chat/compact", Compaction: chunk.Compaction})
	}
	if chunk.Tool != nil {
		h.publishLocked(t, TurnEvent{Event: "chat/tool", Tool: chunk.Tool})
	}
	if chunk.Steer != nil {
		h.publishLocked(t, TurnEvent{Event: "chat/steer", Steer: chunk.Steer})
	}
	if chunk.Budget != nil {
		h.publishLocked(t, TurnEvent{Event: "chat/budget", Budget: chunk.Budget})
	}
	if chunk.Hook != nil {
		h.publishLocked(t, TurnEvent{Event: "chat/hook", Hook: chunk.Hook})
	}
	if chunk.Approval != nil {
		if chunk.Approval.State == permission.StatePending {
			t.waiting[chunk.Approval.ID] = true
		} else {
			delete(t.waiting, chunk.Approval.ID)
		}
		t.info.AwaitingApproval = len(t.waiting) > 0
		h.publishLocked(t, TurnEvent{Event: "chat/approval", Approval: chunk.Approval})
	}
}

func (h *Hub) finish(t *hubTurn, out agent.ChatOutcome, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	t.info.State = TurnDone
	t.info.EndedAt = time.Now().UnixMilli()
	t.info.Reply = out.Reply
	if t.info.Reply == "" {
		t.info.Reply = t.reply.String()
	}
	t.info.Stopped = t.info.Stopped || out.Stopped
	switch {
	case err != nil:
		t.info.Error = err.Error()
	case t.errMsg != "":
		t.info.Error = t.errMsg
	}
	t.log = nil
	if h.active[t.info.SessionID] == t {
		delete(h.active, t.info.SessionID)
	}
	h.publishLocked(t, TurnEvent{Event: "turn/done", Turn: infoPtr(t.info)})
	t.result <- t.info
	close(t.finished)
	h.queueLeftoverSteerLocked(t)
	h.pumpLocked()
}

// publishLocked stamps, records (for live turns) and broadcasts.
func (h *Hub) publishLocked(t *hubTurn, e TurnEvent) {
	h.seq++
	e.Seq = h.seq
	e.SessionID = t.info.SessionID
	e.TurnID = t.info.TurnID
	if t.info.State == TurnRunning && strings.HasPrefix(e.Event, "chat/") {
		t.record(e)
	}
	h.sink(e)
}

// record keeps the replay log compact: consecutive deltas merge and a
// tool's later updates replace its first row in place.
func (t *hubTurn) record(e TurnEvent) {
	if e.Event == "chat/delta" && len(t.log) > 0 {
		last := &t.log[len(t.log)-1]
		if last.Event == "chat/delta" {
			last.Delta += e.Delta
			last.Seq = e.Seq
			return
		}
	}
	if key := replaceKey(e); key != "" {
		if i, ok := t.toolAt[key]; ok {
			t.log[i] = e
			return
		}
		t.toolAt[key] = len(t.log)
	}
	t.log = append(t.log, e)
}

// replaceKey names events whose later updates replace the first one.
func replaceKey(e TurnEvent) string {
	switch {
	case e.Event == "chat/tool" && e.Tool != nil && e.Tool.ID != "":
		return e.Tool.ID
	case e.Event == "chat/approval" && e.Approval != nil && e.Approval.ID != "":
		return "approval:" + e.Approval.ID
	}
	return ""
}

// Attach snapshots a session atomically with history: events published
// after it carry a larger Seq than the snapshot.
func (h *Hub) Attach(sessionID string, history func() (any, error)) (Attachment, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var att Attachment
	if history != nil {
		hist, err := history()
		if err != nil {
			return Attachment{}, err
		}
		att.History = hist
	}
	att.Seq = h.seq
	att.Events = []TurnEvent{}
	att.Queued = []TurnInfo{}
	if t := h.active[sessionID]; t != nil {
		att.Turn = infoPtr(t.info)
		if !t.info.Persisted {
			att.Events = append(att.Events, t.log...)
		}
	}
	for _, t := range h.queue {
		if t.info.SessionID == sessionID {
			att.Queued = append(att.Queued, t.info)
		}
	}
	return att, nil
}

// List reports every running and queued turn.
func (h *Hub) List() []TurnInfo {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := []TurnInfo{}
	for _, t := range h.active {
		out = append(out, t.info)
	}
	for _, t := range h.queue {
		out = append(out, t.info)
	}
	return out
}

// Running reports whether the session has a turn in flight.
func (h *Hub) Running(sessionID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.active[sessionID] != nil
}

// Busy reports whether the session has a turn running or queued.
func (h *Hub) Busy(sessionID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.active[sessionID] != nil {
		return true
	}
	for _, t := range h.queue {
		if t.info.SessionID == sessionID {
			return true
		}
	}
	return false
}

// StopSession stops the session's running turn and drops its queue.
// An empty session id applies to every session in cwd (all when cwd is
// empty too). Reports whether anything was stopped or dropped.
func (h *Hub) StopSession(sessionID, cwd string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	match := func(t *hubTurn) bool {
		if sessionID != "" {
			return t.info.SessionID == sessionID
		}
		return cwd == "" || t.info.Cwd == cwd
	}
	hit := h.dropQueuedLocked(match)
	for _, t := range h.active {
		if match(t) {
			t.halt()
			hit = true
		}
	}
	return hit
}

// StopRunning stops the session's running turn, leaving its queue.
func (h *Hub) StopRunning(sessionID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if t := h.active[sessionID]; t != nil {
		t.halt()
		return true
	}
	return false
}

// Cancel drops a queued turn, or stops it when already running.
func (h *Hub) Cancel(turnID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.dropQueuedLocked(func(t *hubTurn) bool { return t.info.TurnID == turnID }) {
		return true
	}
	for _, t := range h.active {
		if t.info.TurnID == turnID {
			t.halt()
			return true
		}
	}
	return false
}

func (h *Hub) dropQueuedLocked(match func(*hubTurn) bool) bool {
	hit := false
	kept := h.queue[:0]
	var dropped []*hubTurn
	for _, t := range h.queue {
		if match(t) {
			dropped = append(dropped, t)
			continue
		}
		kept = append(kept, t)
	}
	for i := len(kept); i < len(h.queue); i++ {
		h.queue[i] = nil
	}
	h.queue = kept
	for _, t := range dropped {
		hit = true
		t.info.State = TurnDone
		t.info.Cancelled = true
		t.info.EndedAt = time.Now().UnixMilli()
		h.publishLocked(t, TurnEvent{Event: "turn/done", Turn: infoPtr(t.info)})
		t.result <- t.info
		close(t.finished)
	}
	return hit
}

// WaitIdle blocks until the session has no running turn or the timeout passes.
func (h *Hub) WaitIdle(sessionID string, timeout time.Duration) bool {
	h.mu.Lock()
	t := h.active[sessionID]
	h.mu.Unlock()
	if t == nil {
		return true
	}
	select {
	case <-t.finished:
		return true
	case <-time.After(timeout):
		return false
	}
}

// Shutdown refuses new turns, stops every turn and waits (bounded) for
// them to persist.
func (h *Hub) Shutdown(timeout time.Duration) {
	h.mu.Lock()
	h.closed = true
	h.mu.Unlock()
	h.StopSession("", "")
	waited := make(chan struct{})
	go func() {
		h.running.Wait()
		close(waited)
	}()
	select {
	case <-waited:
	case <-time.After(timeout):
	}
}

func infoPtr(info TurnInfo) *TurnInfo { return &info }

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
