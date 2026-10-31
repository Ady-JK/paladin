package runtime

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/agent"
	"github.com/Ady-JK/paladin/core/internal/tools"
)

func TestBrowserToolRoundTripsThroughRuntimeAndReturnsImage(t *testing.T) {
	const pixel = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/lXcAAAAASUVORK5CYII="
	var rounds atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if rounds.Add(1) == 1 {
			args := `{"action":"screenshot"}`
			fmt.Fprintf(w, "data: %s\n\n", fmt.Sprintf(`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c-browser","function":{"name":"browser","arguments":%q}}]}}]}`, args))
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode model input: %v", err)
		}
		encoded, _ := json.Marshal(body["messages"])
		if !strings.Contains(string(encoded), "data:image/png;base64,"+pixel) {
			t.Errorf("model follow-up did not receive the screenshot as image input: %s", encoded)
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"saw screenshot\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	rt, workspace := testRuntime(t)
	if err := rt.store.Keys.Set("openrouter", "sk-test"); err != nil {
		t.Fatal(err)
	}
	pointMockAt(rt, server.URL)
	sess, err := rt.OpenSession("", workspace)
	if err != nil {
		t.Fatal(err)
	}
	var got tools.BrowserRequest
	rt.SetBrowserSink(func(req tools.BrowserRequest) {
		got = req
		if !rt.BrowserReply(tools.BrowserReply{ID: req.ID, OK: true, Result: json.RawMessage(`{"url":"http://127.0.0.1:8080"}`), ImageBase64: pixel}) {
			t.Error("browser reply was not accepted")
		}
	})
	outcome, _ := chatToEnd(t, rt, agent.Request{SessionID: sess.SessionID, Message: "inspect the UI", Cwd: workspace})
	if got.SessionID != sess.SessionID || got.Action != "screenshot" || outcome.Reply != "saw screenshot" || rounds.Load() != 2 {
		t.Fatalf("browser round trip failed: request=%+v outcome=%+v rounds=%d", got, outcome, rounds.Load())
	}
}
