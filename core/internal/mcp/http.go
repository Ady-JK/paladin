package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// maxHTTPBody caps one response body.
const maxHTTPBody = 64 << 20

// ErrSessionExpired means the server forgot our Mcp-Session-Id; the
// connection is dead and the manager reconnects.
var ErrSessionExpired = errors.New("server ended the MCP session (HTTP 404)")

// httpTransport speaks Streamable HTTP: every message is a POST; the
// reply is JSON or an SSE stream that may carry server notifications
// and requests before the response.
type httpTransport struct {
	url     string
	headers map[string]string
	client  *http.Client
	deliver func([]byte)
	fail    func(error)

	mu      sync.Mutex
	session string
	version string
	closed  bool
}

func newHTTP(cfg ServerConfig, deliver func([]byte), fail func(error)) (*httpTransport, error) {
	url := strings.TrimSpace(cfg.URL)
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return nil, fmt.Errorf("url %q must start with http:// or https://", url)
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DialContext = (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	tr.ResponseHeaderTimeout = 0
	return &httpTransport{url: url, headers: cfg.Headers, client: &http.Client{Transport: tr}, deliver: deliver, fail: fail}, nil
}

func (t *httpTransport) send(ctx context.Context, msg []byte) error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return ErrClosed
	}
	session, version := t.session, t.version
	t.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, bytes.NewReader(msg))
	if err != nil {
		return err
	}
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if session != "" {
		req.Header.Set("Mcp-Session-Id", session)
	}
	if version != "" {
		req.Header.Set("MCP-Protocol-Version", version)
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		t.mu.Lock()
		t.session = sid
		t.mu.Unlock()
	}
	switch {
	case resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusNoContent:
		return nil
	case resp.StatusCode == http.StatusNotFound && session != "":
		t.fail(ErrSessionExpired)
		return ErrSessionExpired
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return httpStatusError(resp.StatusCode, strings.TrimSpace(string(body)))
	}
	body := io.LimitReader(resp.Body, maxHTTPBody)
	ctype := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.HasPrefix(ctype, "text/event-stream") {
		return t.readSSE(body)
	}
	data, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	t.deliverJSON(data)
	return nil
}

func httpStatusError(code int, body string) error {
	msg := fmt.Sprintf("HTTP %d %s", code, http.StatusText(code))
	switch code {
	case http.StatusUnauthorized, http.StatusForbidden:
		msg += " (check the server's headers, e.g. Authorization)"
	}
	if body != "" {
		msg += ": " + truncate(body, 300)
	}
	return errors.New(msg)
}

// deliverJSON hands on one message or a JSON-RPC batch.
func (t *httpTransport) deliverJSON(data []byte) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return
	}
	if data[0] == '[' {
		var batch []json.RawMessage
		if json.Unmarshal(data, &batch) == nil {
			for _, m := range batch {
				t.deliver(m)
			}
			return
		}
	}
	t.deliver(data)
}

// readSSE delivers each event's data until the stream ends.
func (t *httpTransport) readSSE(r io.Reader) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), maxHTTPBody)
	var data []string
	flush := func() {
		if len(data) > 0 {
			t.deliverJSON([]byte(strings.Join(data, "\n")))
			data = data[:0]
		}
	}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	flush()
	return sc.Err()
}

// close ends the server-side session (best effort) and the transport.
func (t *httpTransport) close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	session := t.session
	t.mu.Unlock()
	if session != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if req, err := http.NewRequestWithContext(ctx, http.MethodDelete, t.url, nil); err == nil {
			for k, v := range t.headers {
				req.Header.Set(k, v)
			}
			req.Header.Set("Mcp-Session-Id", session)
			if resp, err := t.client.Do(req); err == nil {
				_ = resp.Body.Close()
			}
		}
	}
	t.client.CloseIdleConnections()
	return nil
}

func (t *httpTransport) pid() int { return 0 }

func (t *httpTransport) setProtocolVersion(v string) {
	t.mu.Lock()
	t.version = v
	t.mu.Unlock()
}

func (t *httpTransport) sessionID() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.session
}
