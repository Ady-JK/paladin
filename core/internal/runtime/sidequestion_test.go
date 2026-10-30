package runtime

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/agent"
	"github.com/Ady-JK/paladin/core/internal/inference"
)

// reply answers a streamed turn with SSE and a buffered call with JSON.
func reply(w http.ResponseWriter, body []byte, text string, cost float64) {
	if !strings.Contains(string(body), `"stream":true`) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"choices":[{"message":{"content":%q}}],"usage":{"prompt_tokens":5,"completion_tokens":5,"total_tokens":10,"cost":%v}}`, text, cost)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", text)
	fmt.Fprintf(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":5,\"total_tokens\":10,\"cost\":%v}}\n\ndata: [DONE]\n\n", cost)
}

func TestSideQuestionAnswersFromHistoryWithoutTouchingTheTranscript(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(body))
		n := len(bodies)
		mu.Unlock()
		text := "the main answer"
		if n > 1 {
			text = "side answer"
		}
		reply(w, body, text, 0.25)
	}))
	t.Cleanup(srv.Close)
	rt, _ := testRuntime(t)
	if err := rt.store.Keys.Set("openrouter", "sk-test"); err != nil {
		t.Fatal(err)
	}
	pointMockAt(rt, srv.URL)
	ws := t.TempDir()
	sess, err := rt.OpenSession("", ws)
	if err != nil {
		t.Fatal(err)
	}
	chatToEnd(t, rt, agent.Request{SessionID: sess.SessionID, Message: "build it", Cwd: ws})
	before, _ := rt.SessionUsage(sess.SessionID, ws)
	loaded, _ := rt.transcripts.Load(ws, sess.SessionID)
	history, _ := loaded.History()

	got, err := rt.SideQuestion(context.Background(), SideQuestionRequest{SessionID: sess.SessionID, Cwd: ws, Question: "what file did you edit?"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Answer != "side answer" {
		t.Fatalf("answer %+v", got)
	}
	mu.Lock()
	body := bodies[len(bodies)-1]
	mu.Unlock()
	for _, want := range []string{"the main answer", "build it", "what file did you edit?", "side question"} {
		if !strings.Contains(body, want) {
			t.Fatalf("request lacks %q: %s", want, body)
		}
	}
	if strings.Contains(body, `"tools"`) {
		t.Fatalf("side questions get no tools: %s", body)
	}
	loaded, _ = rt.transcripts.Load(ws, sess.SessionID)
	if after, _ := loaded.History(); len(after) != len(history) {
		t.Fatalf("transcript changed: %d -> %d messages", len(history), len(after))
	}
	if rt.liveConversation(sess.SessionID) != nil {
		t.Fatal("a finished turn must not leave a live snapshot")
	}
	after, _ := rt.SessionUsage(sess.SessionID, ws)
	if after.Calls != before.Calls+1 || after.Cost < before.Cost+0.25-1e-9 {
		t.Fatalf("side question not billed: before %+v after %+v", before.Counters, after.Counters)
	}
}

func TestSideQuestionValidates(t *testing.T) {
	rt, _ := testRuntime(t)
	if err := rt.store.Keys.Set("openrouter", "sk-test"); err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	sess, err := rt.OpenSession("", ws)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.SideQuestion(context.Background(), SideQuestionRequest{SessionID: sess.SessionID, Cwd: ws, Question: "  "}); err == nil {
		t.Fatal("blank question must fail")
	}
	if _, err := rt.SideQuestion(context.Background(), SideQuestionRequest{SessionID: "nope", Cwd: ws, Question: "q"}); err == nil {
		t.Fatal("unknown session must fail")
	}
}

func TestSideQuestionMidTurnSeesTheLiveConversation(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		reply(w, b, "still on step 2", 0)
	}))
	t.Cleanup(srv.Close)
	rt, _ := testRuntime(t)
	if err := rt.store.Keys.Set("openrouter", "sk-test"); err != nil {
		t.Fatal(err)
	}
	pointMockAt(rt, srv.URL)
	ws := t.TempDir()
	sess, err := rt.OpenSession("", ws)
	if err != nil {
		t.Fatal(err)
	}
	rt.setLiveConversation(sess.SessionID, []inference.LlmMessage{{Role: "user", Content: "refactor the parser"}, {Role: "assistant", Content: "step 1 done, starting step 2"}})
	got, err := rt.SideQuestion(context.Background(), SideQuestionRequest{SessionID: sess.SessionID, Cwd: ws, Question: "how far along?"})
	if err != nil || got.Answer != "still on step 2" {
		t.Fatalf("%+v %v", got, err)
	}
	if !strings.Contains(body, "starting step 2") {
		t.Fatalf("live conversation missing: %s", body)
	}
}

func TestSideQuestionClosesToolCallsStillRunning(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		reply(w, b, "running the tests", 0)
	}))
	t.Cleanup(srv.Close)
	rt, _ := testRuntime(t)
	if err := rt.store.Keys.Set("openrouter", "sk-test"); err != nil {
		t.Fatal(err)
	}
	pointMockAt(rt, srv.URL)
	ws := t.TempDir()
	sess, err := rt.OpenSession("", ws)
	if err != nil {
		t.Fatal(err)
	}
	id, name := "c1", "read"
	rt.setLiveConversation(sess.SessionID, []inference.LlmMessage{
		{Role: "user", Content: "run tests"},
		{Role: "assistant", ToolCalls: []inference.ToolCall{{ID: "c1", Name: "read", Arguments: "{}"}, {ID: "c2", Name: "shell", Arguments: `{"command":"go test"}`}}},
		{Role: "tool", Content: "file text", ToolCallID: &id, Name: &name},
	})
	if _, err := rt.SideQuestion(context.Background(), SideQuestionRequest{SessionID: sess.SessionID, Cwd: ws, Question: "what now?"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, `"tool_call_id":"c2"`) || !strings.Contains(body, "still running") {
		t.Fatalf("in-flight call c2 should get a placeholder result: %s", body)
	}
	if strings.Count(body, `"tool_call_id":"c1"`) != 1 {
		t.Fatalf("finished call c1 must keep only its real result: %s", body)
	}
}
