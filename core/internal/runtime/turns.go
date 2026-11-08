package runtime

import (
	"fmt"
	"strings"
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
// A subagent session only takes turns from its parent.
func (r *AgentRuntime) SubmitTurn(req agent.Request, opts SubmitOpts) (TurnInfo, <-chan TurnInfo, error) {
	if len(req.AttachmentIDs) > 12 {
		return TurnInfo{}, nil, fmt.Errorf("too many attachments")
	}
	for _, id := range req.AttachmentIDs {
		ref, err := r.SessionAttachment(req.SessionID, req.Cwd, id)
		if err != nil {
			return TurnInfo{}, nil, err
		}
		req.Attachments = append(req.Attachments, *ref)
		req.Message += fmt.Sprintf("\n[Attached file: %q at %q]", ref.Name, ref.Path)
	}
	if err := r.subagents.refuseUserTurn(req.SessionID, req.Cwd); err != nil {
		return TurnInfo{}, nil, err
	}
	return r.hub.Submit(req, opts)
}

// SteerTurn sends a message into the session's running turn (see
// Hub.Steer). A subagent's window accepts a steer like any other running
// turn — the user is watching it work and may guide it — but it still
// cannot be started by the user: SubmitTurn keeps refusing a child.
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
// HistoryLimit is how many transcript rows an attach paints at once;
// older rows load in pages.
const HistoryLimit = 120

// Attach joins a session with its newest history page. limit <= 0 uses
// HistoryLimit; a subagent side pane asks for more because it has no
// "load earlier" control.
func (r *AgentRuntime) Attach(sessionID, cwd string, limit int) (Attachment, error) {
	if limit <= 0 {
		limit = HistoryLimit
	}
	return r.hub.Attach(sessionID, func() (any, error) {
		return r.SessionHistoryPage(sessionID, cwd, limit, 0)
	})
}

// ShutdownTurns stops everything and waits (bounded) for turns to
// persist, then stops the MCP and language servers.
func (r *AgentRuntime) ShutdownTurns(timeout time.Duration) {
	r.sideCancel()
	r.hub.Shutdown(timeout)
	r.mcp.Close()
	r.lsp.Close()
	r.editorLsp.Close()
	// Plugins get a shutdown line and a short grace; nothing may keep the
	// app from exiting.
	r.extensions.Close()
}

// ListAgents lists a session's subagents with their live status.
func (r *AgentRuntime) ListAgents(sessionID string) ([]AgentInfo, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, fmt.Errorf("sessionId is required")
	}
	return r.subagents.list(sessionID)
}

// InterruptAgent stops one of a session's subagents (its running turn
// and queued follow-ups); the parent is told when it next looks.
func (r *AgentRuntime) InterruptAgent(sessionID, agentID string) (AgentInfo, error) {
	if strings.TrimSpace(sessionID) == "" {
		return AgentInfo{}, fmt.Errorf("sessionId is required")
	}
	info, _, err := r.subagents.interrupt(sessionID, agentID)
	return info, err
}
