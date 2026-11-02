// sse.go filters SSE frames the OpenAI SDK cannot parse.
//
// The SDK's stream decoder json.Unmarshals every event's data field
// (openai-go/packages/ssestream), so a frame that carries no data — what
// several gateways and proxies emit as a keepalive, e.g. a bare
// "data:" line or an "event: ping" frame — aborts a perfectly healthy
// stream with "unexpected end of JSON input". The model reply is lost
// mid-sentence and every retry hits the next keepalive.
//
// The filter drops frames with no data payload and passes every other
// byte through untouched, so a ping can never truncate a reply.

package inference

import (
	"bufio"
	"bytes"
	"io"
	"net/http"
	"strings"

	"github.com/openai/openai-go/option"
)

// sseMaxFrame bounds one buffered frame. A frame larger than this is
// passed through unfiltered rather than held in memory.
const sseMaxFrame = 1 << 20

// skipEmptySSEFrames is the client middleware that installs the filter
// on event-stream responses only; JSON replies are untouched.
func skipEmptySSEFrames() option.Middleware {
	return func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		resp, err := next(req)
		if err != nil || resp == nil || resp.Body == nil {
			return resp, err
		}
		ctype := strings.ToLower(resp.Header.Get("Content-Type"))
		if !strings.Contains(ctype, "text/event-stream") {
			return resp, nil
		}
		resp.Body = &sseFilter{rc: resp.Body, br: bufio.NewReader(resp.Body)}
		return resp, nil
	}
}

// sseFilter is a ReadCloser that drops SSE frames with no data payload.
// Everything else streams through in order.
type sseFilter struct {
	rc   io.ReadCloser
	br   *bufio.Reader
	out  bytes.Buffer
	done bool
}

func (f *sseFilter) Read(p []byte) (int, error) {
	for f.out.Len() == 0 && !f.done {
		if err := f.fill(); err != nil {
			f.done = true
			if f.out.Len() == 0 {
				return 0, err
			}
			break
		}
	}
	if f.out.Len() == 0 && f.done {
		return 0, io.EOF
	}
	return f.out.Read(p)
}

func (f *sseFilter) Close() error { return f.rc.Close() }

// fill reads one frame (its lines through the blank separator) and
// appends it to out unless it carries no data payload. A frame that
// outgrows sseMaxFrame is passed through rather than buffered further.
func (f *sseFilter) fill() error {
	var frame bytes.Buffer
	hasData := false
	for {
		line, err := f.br.ReadString('\n')
		if line != "" {
			frame.WriteString(line)
			if payload, ok := sseDataPayload(line); ok && payload != "" {
				hasData = true
			}
			if isSSEBlankLine(line) || frame.Len() > sseMaxFrame {
				if hasData || frame.Len() > sseMaxFrame {
					f.out.Write(frame.Bytes())
				}
				return nil
			}
		}
		if err != nil {
			// A cut-off frame cannot be filtered safely: hand it to the
			// SDK as it arrived so it reports the real problem.
			if frame.Len() > 0 && hasData {
				f.out.Write(frame.Bytes())
			}
			return err
		}
	}
}

// sseDataPayload reports whether line is a data field and returns its
// value with the single optional space after the colon removed.
func sseDataPayload(line string) (string, bool) {
	if !strings.HasPrefix(line, "data:") {
		return "", false
	}
	value := strings.TrimSuffix(strings.TrimSuffix(line[len("data:"):], "\n"), "\r")
	value = strings.TrimPrefix(value, " ")
	return strings.TrimSpace(value), true
}

// isSSEBlankLine reports whether line ends a frame.
func isSSEBlankLine(line string) bool {
	return line == "\n" || line == "\r\n" || line == ""
}
