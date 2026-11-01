// Package lsp is a small Language Server Protocol client: it starts
// installed language servers (never downloads one), tells them about
// files the agent changed and collects the diagnostics they publish, so
// write and edit can report compile and type errors right away.
//
// conn.go is the JSON-RPC 2.0 transport with LSP's Content-Length
// framing over a server's stdin/stdout.
package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
)

// maxMessage bounds one inbound message so a broken server cannot
// exhaust memory.
const maxMessage = 32 << 20

// maxDocBytes is the largest file sent to a language server.
const maxDocBytes = 1 << 20

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("lsp error %d: %s", e.Code, e.Message) }

// errClosed is returned once the connection is gone.
var errClosed = errors.New("lsp: connection closed")

// conn is one JSON-RPC session. onNotify receives server notifications;
// onRequest answers server requests (nil result means null).
type conn struct {
	w         io.Writer
	writeMu   sync.Mutex
	mu        sync.Mutex
	nextID    int
	pending   map[int]chan message
	closed    chan struct{}
	closeOnce sync.Once
	onNotify  func(method string, params json.RawMessage)
	onRequest func(method string, params json.RawMessage) any
}

func newConn(r io.Reader, w io.Writer, onNotify func(string, json.RawMessage), onRequest func(string, json.RawMessage) any) *conn {
	c := &conn{w: w, pending: map[int]chan message{}, closed: make(chan struct{}), onNotify: onNotify, onRequest: onRequest}
	go c.readLoop(bufio.NewReaderSize(r, 64*1024))
	return c
}

// close marks the connection gone. Pending calls see c.closed; their
// response channels are never closed, so the read loop can never send
// into a closed one.
func (c *conn) close() {
	c.closeOnce.Do(func() { close(c.closed) })
}

func (c *conn) send(m message) error {
	m.JSONRPC = "2.0"
	body, err := json.Marshal(m)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	select {
	case <-c.closed:
		return errClosed
	default:
	}
	if _, err := fmt.Fprintf(c.w, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return err
	}
	_, err = c.w.Write(body)
	return err
}

// call sends a request and waits for its response.
func (c *conn) call(ctx context.Context, method string, params, result any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	ch := make(chan message, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()
	if err := c.send(message{ID: json.RawMessage(strconv.Itoa(id)), Method: method, Params: raw}); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.closed:
		return errClosed
	case resp := <-ch:
		if resp.Error != nil {
			return resp.Error
		}
		if result != nil && len(resp.Result) > 0 {
			return json.Unmarshal(resp.Result, result)
		}
		return nil
	}
}

// notify sends a notification.
func (c *conn) notify(method string, params any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return c.send(message{Method: method, Params: raw})
}

func (c *conn) readLoop(r *bufio.Reader) {
	defer c.close()
	for {
		body, err := readFrame(r)
		if err != nil {
			return
		}
		var m message
		if json.Unmarshal(body, &m) != nil {
			continue
		}
		switch {
		case m.Method != "" && len(m.ID) > 0:
			var result any
			if c.onRequest != nil {
				result = c.onRequest(m.Method, m.Params)
			}
			raw, _ := json.Marshal(result)
			_ = c.send(message{ID: m.ID, Result: raw})
		case m.Method != "":
			if c.onNotify != nil {
				c.onNotify(m.Method, m.Params)
			}
		case len(m.ID) > 0:
			id, err := strconv.Atoi(string(m.ID))
			if err != nil {
				continue
			}
			c.mu.Lock()
			ch := c.pending[id]
			delete(c.pending, id)
			c.mu.Unlock()
			// The buffer holds the one response a call waits for; a
			// duplicate from a broken server is dropped, not blocked on.
			if ch != nil {
				select {
				case ch <- m:
				default:
				}
			}
		}
	}
}

// readFrame reads one Content-Length framed message body.
func readFrame(r *bufio.Reader) ([]byte, error) {
	length := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		name, value, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			n, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return nil, fmt.Errorf("lsp: bad Content-Length %q", value)
			}
			length = n
		}
	}
	if length < 0 || length > maxMessage {
		return nil, fmt.Errorf("lsp: missing or oversized Content-Length %d", length)
	}
	body := make([]byte, length)
	_, err := io.ReadFull(r, body)
	return body, err
}
