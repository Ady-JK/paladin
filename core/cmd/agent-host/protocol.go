package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"github.com/Ady-JK/paladin/core/internal/jsonx"
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
	handle, ok := methods[req.Method]
	if !ok {
		return errResponse(req.ID, fmt.Sprintf("unknown method %q", req.Method))
	}
	result, err := handle(rt, parseArgs(req.Params))
	if err != nil {
		return errResponse(req.ID, err.Error())
	}
	return okResponse(req.ID, result)
}

// installTurnSink streams every hub event (chat/* and turn/*) to the client.
func installTurnSink(rt *runtime.AgentRuntime) {
	rt.SetTurnSink(func(e runtime.TurnEvent) { emit(e) })
}
