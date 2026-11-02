// sse_test.go covers the keepalive filter: frames with no data payload
// (gateway pings) are dropped so the SDK's decoder never json-unmarshals
// an empty event, while real events and their bytes pass through.

package inference

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func readAllFiltered(t *testing.T, body string) string {
	t.Helper()
	f := &sseFilter{rc: io.NopCloser(strings.NewReader(body)), br: bufio.NewReader(strings.NewReader(body))}
	out, err := io.ReadAll(f)
	if err != nil && err != io.EOF {
		t.Fatalf("read: %v", err)
	}
	return string(out)
}

func TestSSEFilterDropsEmptyDataFrames(t *testing.T) {
	in := "data: {\"a\":1}\n\n" +
		"data:\n\n" + // gateway keepalive: empty payload
		"data:   \n\n" + // whitespace only
		"event: ping\n\n" + // comment/event frame with no data
		": keepalive\n\n" + // SSE comment
		"data: {\"b\":2}\n\n"
	got := readAllFiltered(t, in)
	want := "data: {\"a\":1}\n\ndata: {\"b\":2}\n\n"
	if got != want {
		t.Fatalf("filtered stream wrong:\n got %q\nwant %q", got, want)
	}
}

func TestSSEFilterKeepsDoneMarker(t *testing.T) {
	in := "data: {\"a\":1}\n\ndata: [DONE]\n\n"
	if got := readAllFiltered(t, in); got != in {
		t.Fatalf("a [DONE] frame carries data and must pass: %q", got)
	}
}

func TestSSEFilterKeepsCRLFFrames(t *testing.T) {
	in := "data: {\"a\":1}\r\n\r\ndata:\r\n\r\ndata: {\"b\":2}\r\n\r\n"
	want := "data: {\"a\":1}\r\n\r\ndata: {\"b\":2}\r\n\r\n"
	if got := readAllFiltered(t, in); got != want {
		t.Fatalf("CRLF frames mangled:\n got %q\nwant %q", got, want)
	}
}

func TestSSEFilterPassesTailWithoutBlankLine(t *testing.T) {
	in := "data: {\"a\":1}\n\ndata: {\"b\":2}\n"
	if got := readAllFiltered(t, in); got != in {
		t.Fatalf("unterminated frame must pass through: %q", got)
	}
}

func TestSSEFilterOnlyWrapsEventStreams(t *testing.T) {
	calls := 0
	mw := skipEmptySSEFrames()
	next := func(req *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
		}, nil
	}
	resp, err := mw(&http.Request{}, next)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || string(body) != `{"ok":true}` {
		t.Fatalf("JSON body must pass unchanged: %q", body)
	}
	if _, wrapped := resp.Body.(*sseFilter); wrapped {
		t.Fatal("plain JSON response must not be wrapped")
	}
}

// TestStreamSurvivesGatewayKeepalives is the end-to-end proof through
// the real SDK client: a keepalive frame with no data mid-reply must
// not end the stream with "unexpected end of JSON input".
func TestStreamSurvivesGatewayKeepalives(t *testing.T) {
	sse := "data: {\"choices\":[{\"delta\":{\"content\":\"Hel\"}}]}\n\n" +
		"data:\n\n" + // gateway ping, exactly what killed the stream
		"event: ping\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"lo\"}}]}\n\n" +
		"data: [DONE]\n\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sse)
	}))
	defer server.Close()
	p := providerFor(server.URL)
	tx := make(chan Chunk, 64)
	done := make(chan struct{})
	defer close(done)
	if err := p.StreamTools(context.Background(), "sys", nil, nil, "sess", tx, done); err != nil {
		t.Fatalf("a keepalive must not fail the stream: %v", err)
	}
	close(tx)
	var text strings.Builder
	for chunk := range tx {
		text.WriteString(chunk.Text)
	}
	if text.String() != "Hello" {
		t.Fatalf("reply truncated by a keepalive: %q", text.String())
	}
}
