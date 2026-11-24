package lsp

import (
	"io"
	"testing"
	"time"
)

// A server that stops reading its input must not hold every edit behind a
// blocked write: the write gives up, the connection is retired and the
// owner is told so it can kill the process.
func TestSendToAServerThatStoppedReadingGivesUp(t *testing.T) {
	old := writeTimeout
	writeTimeout = 200 * time.Millisecond
	t.Cleanup(func() { writeTimeout = old })
	r, w := io.Pipe() // nobody reads w's other end
	defer r.Close()
	out, outW := io.Pipe() // a server that stays quiet but alive
	defer outW.Close()
	stalled := make(chan struct{})
	c := newConn(out, w, nil, nil)
	c.onStall = func() { close(stalled) }
	start := time.Now()
	err := c.send(message{Method: "textDocument/didChange", Params: []byte(`{}`)})
	if err == nil || time.Since(start) > 3*time.Second {
		t.Fatalf("err=%v after %v", err, time.Since(start))
	}
	select {
	case <-stalled:
	case <-time.After(time.Second):
		t.Fatal("the owner was not told")
	}
	select {
	case <-c.closed:
	default:
		t.Fatal("the connection stayed open")
	}
}
