// Command mcp-dummy is a tiny MCP server for tests and manual checks.
// It speaks newline-delimited JSON-RPC on stdio by default, or
// Streamable HTTP with -http ADDR (endpoint /mcp).
//
// Tools: echo (read-only), add (structured result), slow (cancellable
// sleep), fail (isError), crash (exits the process), image, grow (adds
// a tool and announces tools/list_changed) and ping_client (a server ->
// client request; stdio only).
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
)

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

var supported = map[string]bool{"2025-06-18": true, "2025-03-26": true, "2024-11-05": true}

type server struct {
	pageSize int
	hangInit bool
	sseReply bool

	mu       sync.Mutex
	grown    bool
	cancels  map[string]chan struct{}
	waiters  map[string]chan json.RawMessage
	nextID   int
	send     func(message)
	sessions map[string]bool
}

func main() {
	httpAddr := flag.String("http", "", "serve Streamable HTTP on this address instead of stdio")
	pageSize := flag.Int("page-size", 0, "tools/list page size (0 = one page)")
	hangInit := flag.Bool("hang-init", false, "never answer initialize")
	sseReply := flag.Bool("sse", false, "HTTP: answer requests as an SSE stream")
	flag.Parse()
	s := &server{pageSize: *pageSize, hangInit: *hangInit, sseReply: *sseReply,
		cancels: map[string]chan struct{}{}, waiters: map[string]chan json.RawMessage{}, sessions: map[string]bool{}}
	fmt.Fprintln(os.Stderr, "mcp-dummy: ready")
	if *httpAddr != "" {
		s.serveHTTP(*httpAddr)
		return
	}
	s.serveStdio()
}

func (s *server) serveStdio() {
	var outMu sync.Mutex
	enc := json.NewEncoder(os.Stdout)
	s.send = func(m message) {
		m.JSONRPC = "2.0"
		outMu.Lock()
		defer outMu.Unlock()
		_ = enc.Encode(m)
	}
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var m message
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			continue
		}
		go func() {
			if reply, ok := s.handle(m); ok {
				s.send(reply)
			}
		}()
	}
}

// handle answers one inbound message; ok is false for notifications
// and responses, which get no reply.
func (s *server) handle(m message) (message, bool) {
	if m.Method == "" {
		s.mu.Lock()
		ch := s.waiters[string(m.ID)]
		s.mu.Unlock()
		if ch != nil {
			raw, _ := json.Marshal(m.Result)
			ch <- raw
		}
		return message{}, false
	}
	if len(m.ID) == 0 {
		if m.Method == "notifications/cancelled" {
			var p struct {
				RequestID json.RawMessage `json:"requestId"`
			}
			_ = json.Unmarshal(m.Params, &p)
			s.mu.Lock()
			if ch := s.cancels[string(p.RequestID)]; ch != nil {
				close(ch)
				delete(s.cancels, string(p.RequestID))
			}
			s.mu.Unlock()
		}
		return message{}, false
	}
	reply := message{ID: m.ID}
	switch m.Method {
	case "initialize":
		if s.hangInit {
			select {}
		}
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(m.Params, &p)
		version := p.ProtocolVersion
		if !supported[version] {
			version = "2025-06-18"
		}
		reply.Result = map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": true}},
			"serverInfo":      map[string]any{"name": "mcp-dummy", "version": "1.0.0"},
			"instructions":    "Dummy server for Paladin tests.",
		}
	case "ping":
		reply.Result = map[string]any{}
	case "tools/list":
		reply.Result = s.list(m.Params)
	case "tools/call":
		reply.Result, reply.Error = s.call(m)
	default:
		reply.Error = &rpcError{Code: -32601, Message: "method not found: " + m.Method}
	}
	return reply, true
}

func (s *server) tools() []map[string]any {
	obj := func(props map[string]any, required ...string) map[string]any {
		out := map[string]any{"type": "object", "properties": props}
		if len(required) > 0 {
			out["required"] = required
		}
		return out
	}
	str := map[string]any{"type": "string"}
	num := map[string]any{"type": "number"}
	list := []map[string]any{
		{"name": "echo", "description": "Echo the text back.", "inputSchema": obj(map[string]any{"text": str}, "text"),
			"annotations": map[string]any{"readOnlyHint": true}},
		{"name": "add", "description": "Add two numbers.", "inputSchema": obj(map[string]any{"a": num, "b": num}, "a", "b"),
			"annotations": map[string]any{"readOnlyHint": true}},
		{"name": "slow", "description": "Sleep for ms milliseconds.", "inputSchema": obj(map[string]any{"ms": num})},
		{"name": "fail", "description": "Always fails.", "inputSchema": map[string]any{"$schema": "http://json-schema.org/draft-07/schema#"}},
		{"name": "crash", "description": "Exit the server process.", "inputSchema": obj(map[string]any{}),
			"annotations": map[string]any{"destructiveHint": true}},
		{"name": "image", "description": "Return a tiny PNG.", "inputSchema": obj(map[string]any{}),
			"annotations": map[string]any{"readOnlyHint": true}},
		{"name": "grow", "description": "Add the extra tool.", "inputSchema": obj(map[string]any{})},
		{"name": "ping_client", "description": "Ping the client.", "inputSchema": obj(map[string]any{})},
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.grown {
		list = append(list, map[string]any{"name": "extra", "description": "Appears after grow.", "inputSchema": obj(map[string]any{}),
			"annotations": map[string]any{"readOnlyHint": true}})
	}
	return list
}

func (s *server) list(params json.RawMessage) map[string]any {
	all := s.tools()
	if s.pageSize <= 0 {
		return map[string]any{"tools": all}
	}
	var p struct {
		Cursor string `json:"cursor"`
	}
	_ = json.Unmarshal(params, &p)
	start, _ := strconv.Atoi(p.Cursor)
	start = min(max(start, 0), len(all))
	end := min(start+s.pageSize, len(all))
	out := map[string]any{"tools": all[start:end]}
	if end < len(all) {
		out["nextCursor"] = strconv.Itoa(end)
	}
	return out
}

func text(t string) map[string]any { return map[string]any{"type": "text", "text": t} }

func (s *server) call(m message) (any, *rpcError) {
	var p struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(m.Params, &p); err != nil {
		return nil, &rpcError{Code: -32602, Message: "bad params"}
	}
	switch p.Name {
	case "echo":
		return map[string]any{"content": []any{text(fmt.Sprint(p.Arguments["text"]))}}, nil
	case "add":
		a, _ := p.Arguments["a"].(float64)
		b, _ := p.Arguments["b"].(float64)
		return map[string]any{"content": []any{text(fmt.Sprint(a + b))}, "structuredContent": map[string]any{"sum": a + b}}, nil
	case "slow":
		ms, _ := p.Arguments["ms"].(float64)
		ch := make(chan struct{})
		s.mu.Lock()
		s.cancels[string(m.ID)] = ch
		s.mu.Unlock()
		select {
		case <-time.After(time.Duration(ms) * time.Millisecond):
			return map[string]any{"content": []any{text("slept")}}, nil
		case <-ch:
			return map[string]any{"content": []any{text("cancelled")}, "isError": true}, nil
		}
	case "fail":
		return map[string]any{"content": []any{text("it broke")}, "isError": true}, nil
	case "crash":
		fmt.Fprintln(os.Stderr, "mcp-dummy: crashing on request")
		os.Exit(3)
	case "image":
		return map[string]any{"content": []any{
			text("here is a picture"),
			map[string]any{"type": "image", "mimeType": "image/png", "data": "iVBORw0KGgo="},
			map[string]any{"type": "resource_link", "uri": "file:///tmp/x.txt", "name": "x.txt"},
		}}, nil
	case "grow":
		s.mu.Lock()
		s.grown = true
		s.mu.Unlock()
		s.send(message{Method: "notifications/tools/list_changed"})
		return map[string]any{"content": []any{text("grew")}}, nil
	case "ping_client":
		s.mu.Lock()
		s.nextID++
		id := json.RawMessage(strconv.Quote("srv-" + strconv.Itoa(s.nextID)))
		ch := make(chan json.RawMessage, 1)
		s.waiters[string(id)] = ch
		s.mu.Unlock()
		s.send(message{ID: id, Method: "ping"})
		select {
		case <-ch:
			return map[string]any{"content": []any{text("client answered")}}, nil
		case <-time.After(5 * time.Second):
			return map[string]any{"content": []any{text("client never answered")}, "isError": true}, nil
		}
	}
	return nil, &rpcError{Code: -32602, Message: "unknown tool: " + p.Name}
}

// serveHTTP is a minimal Streamable HTTP endpoint: POST carries one
// JSON-RPC message; requests are answered as JSON (or an SSE stream
// with -sse); server notifications ride along on SSE replies.
func (s *server) serveHTTP(addr string) {
	var pending []message
	var pmu sync.Mutex
	s.send = func(m message) {
		m.JSONRPC = "2.0"
		pmu.Lock()
		pending = append(pending, m)
		pmu.Unlock()
	}
	http.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodDelete:
			s.mu.Lock()
			delete(s.sessions, r.Header.Get("Mcp-Session-Id"))
			s.mu.Unlock()
			w.WriteHeader(http.StatusOK)
			return
		case http.MethodPost:
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if auth := r.Header.Get("Authorization"); auth != "" && auth != "Bearer dummy-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var m message
		if json.Unmarshal(body, &m) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		sid := r.Header.Get("Mcp-Session-Id")
		if m.Method == "initialize" {
			sid = strconv.FormatInt(time.Now().UnixNano(), 36)
			s.mu.Lock()
			s.sessions[sid] = true
			s.mu.Unlock()
			w.Header().Set("Mcp-Session-Id", sid)
		} else {
			s.mu.Lock()
			known := s.sessions[sid]
			s.mu.Unlock()
			if !known {
				w.WriteHeader(http.StatusNotFound)
				return
			}
		}
		reply, ok := s.handle(m)
		if !ok {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		reply.JSONRPC = "2.0"
		pmu.Lock()
		extra := pending
		pending = nil
		pmu.Unlock()
		if !s.sseReply && len(extra) == 0 {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(reply)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, e := range append(extra, reply) {
			b, _ := json.Marshal(e)
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", b)
		}
	})
	fmt.Fprintln(os.Stderr, "mcp-dummy: http on", addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
