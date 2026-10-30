package runtime

import (
	"time"

	"github.com/Ady-JK/paladin/core/internal/agent"
)

const (
	defaultConcurrentTurns = 4
	maxConcurrentTurnsCap  = 16
)

func (r *AgentRuntime) maxConcurrentTurns() int {
	n := int(r.loadSettings().MaxConcurrentTurns)
	switch {
	case n <= 0:
		return defaultConcurrentTurns
	case n > maxConcurrentTurnsCap:
		return maxConcurrentTurnsCap
	}
	return n
}

// SubmitTurn queues a turn in the hub; it runs as soon as its session
// is idle and a slot is free. The channel yields the final TurnInfo.
func (r *AgentRuntime) SubmitTurn(req agent.Request, opts SubmitOpts) (TurnInfo, <-chan TurnInfo, error) {
	return r.hub.Submit(req, opts)
}

// SteerTurn sends a message into the session's running turn (see
// Hub.Steer).
func (r *AgentRuntime) SteerTurn(sessionID, message, display string) (TurnInfo, error) {
	return r.hub.Steer(sessionID, message, display)
}

// SetTurnSink routes every hub event (see Hub.SetSink).
func (r *AgentRuntime) SetTurnSink(sink func(TurnEvent)) { r.hub.SetSink(sink) }

// Turns lists running and queued turns across sessions.
func (r *AgentRuntime) Turns() []TurnInfo { return r.hub.List() }

// CancelTurn drops a queued turn or stops a running one.
func (r *AgentRuntime) CancelTurn(turnID string) bool { return r.hub.Cancel(turnID) }

// Attach returns the session's history plus its live turn and queue,
// atomically, so a client can render it and resume streaming.
func (r *AgentRuntime) Attach(sessionID, cwd string) (Attachment, error) {
	return r.hub.Attach(sessionID, func() (any, error) {
		return r.SessionHistory(sessionID, cwd)
	})
}

// ShutdownTurns stops everything and waits (bounded) for turns to
// persist, then stops the MCP servers.
func (r *AgentRuntime) ShutdownTurns(timeout time.Duration) {
	r.sideCancel()
	r.hub.Shutdown(timeout)
	r.mcp.Close()
}
