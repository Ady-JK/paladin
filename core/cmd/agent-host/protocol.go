package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"github.com/Ady-JK/paladin/core/internal/jsonx"
	"github.com/Ady-JK/paladin/core/internal/mcp"
	"github.com/Ady-JK/paladin/core/internal/runtime"
)

type Request struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type Response struct {
	ID     json.RawMessage `json:"id"`
	OK     bool            `json:"ok"`
	Result any             `json:"result,omitempty"`
	Error  *string         `json:"error,omitempty"`
}

var stdoutMu sync.Mutex

// emit writes one JSONL line to stdout; responses and turn events share
// the stream, so writes are serialized.
func emit(v any) {
	data := jsonx.MustMarshal(v)
	data = append(data, '\n')
	stdoutMu.Lock()
	defer stdoutMu.Unlock()
	_, _ = os.Stdout.Write(data)
}

func emitResponse(r Response) { emit(r) }

func okResponse(id json.RawMessage, result any) Response {
	return Response{ID: id, OK: true, Result: result}
}

func errResponse(id json.RawMessage, msg string) Response {
	if msg == "" {
		msg = "unknown error"
	}
	return Response{ID: id, OK: false, Error: &msg}
}

// runUnary answers one request through the method table.
func runUnary(rt *runtime.AgentRuntime, req *Request) Response {
	return runHandler(rt, req, parseArgs(req.Params))
}

func runHandler(rt *runtime.AgentRuntime, req *Request, a args) Response {
	handle, ok := methods[req.Method]
	if !ok {
		return errResponse(req.ID, fmt.Sprintf("unknown method %q", req.Method))
	}
	result, err := handle(rt, a)
	if err != nil {
		return errResponse(req.ID, err.Error())
	}
	return okResponse(req.ID, result)
}

// installTurnSink streams every hub event (chat/* and turn/*) to the client.
func installTurnSink(rt *runtime.AgentRuntime) {
	rt.SetTurnSink(func(e runtime.TurnEvent) { emit(e) })
}

// notices carries the rare state events (MCP, tools, agents) to stdout
// in order on its own goroutine, so the code reporting a change never
// waits on a client that is slow to read.
var notices = newOutbox(emit)

// installMCPSink streams MCP server status changes as mcp/status.
func installMCPSink(rt *runtime.AgentRuntime) {
	rt.SetMCPSink(func(s mcp.ServerStatus) { notices.push(runtime.MCPEvent{Event: "mcp/status", Server: s}) })
}

// installToolsSink streams tools/pending events.
func installToolsSink(rt *runtime.AgentRuntime) { rt.SetToolsSink(emitToolsEvent) }

func emitToolsEvent(e runtime.ToolsEvent) { notices.push(e) }

// installAgentsSink streams agents/changed events (subagent spawned or
// changed status).
func installAgentsSink(rt *runtime.AgentRuntime) { rt.SetAgentsSink(emitAgentsEvent) }

func emitAgentsEvent(e runtime.AgentsEvent) { notices.push(e) }

// outbox writes values in push order on one goroutine that runs only
// while there is something to write.
type outbox struct {
	write   func(any)
	mu      sync.Mutex
	queue   []any
	running bool
	idle    *sync.Cond
}

func newOutbox(write func(any)) *outbox {
	o := &outbox{write: write}
	o.idle = sync.NewCond(&o.mu)
	return o
}

func (o *outbox) push(v any) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.queue = append(o.queue, v)
	if !o.running {
		o.running = true
		go o.drain()
	}
}

func (o *outbox) drain() {
	for {
		o.mu.Lock()
		if len(o.queue) == 0 {
			o.running = false
			o.idle.Broadcast()
			o.mu.Unlock()
			return
		}
		batch := o.queue
		o.queue = nil
		o.mu.Unlock()
		for _, v := range batch {
			_ = guard("event write", func() { o.write(v) })
		}
	}
}

// flush waits until everything pushed so far is written.
func (o *outbox) flush() {
	o.mu.Lock()
	defer o.mu.Unlock()
	for o.running {
		o.idle.Wait()
	}
}
