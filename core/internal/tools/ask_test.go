package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func testAskCtx(t *testing.T, sessionID, callID string) *ToolCtx {
	t.Helper()
	return &ToolCtx{
		Workspace: t.TempDir(), SessionDir: t.TempDir(),
		Cancel: context.Background(), SessionID: sessionID, CallID: callID,
	}
}

func waitAskPending(t *testing.T, tool *AskTool, sessionID, callID string, want bool) {
	t.Helper()
	for i := 0; i < 1000; i++ {
		if tool.Pending(sessionID, callID) == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("pending=%v, want %v", !want, want)
}

func TestAskDefinitionNeedsQuestions(t *testing.T) {
	def := NewAskTool().Definition()
	if def.Name != "ask" {
		t.Fatalf("got %q", def.Name)
	}
	data, err := json.Marshal(def.Parameters)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "questions") {
		t.Fatalf("ask parameters lost questions: %s", data)
	}
}

func TestAskRejectsBadArguments(t *testing.T) {
	tool := NewAskTool()
	ctx := testAskCtx(t, "s", "c")
	cases := []struct {
		name string
		args string
		want string
	}{
		{"not json", `{`, "valid JSON"},
		{"no questions", `{}`, "required"},
		{"empty list", `{"questions":[]}`, "required"},
		{"empty question", `{"questions":[{"question":"  "}]}`, "empty"},
		{"too many questions", `{"questions":[` + strings.Repeat(`{"question":"q"},`, 10) + `{"question":"last"}]}`, "at most 10"},
		{"too many options", `{"questions":[{"question":"q","options":["a","b","c","d","e","f","g"]}]}`, "at most 6"},
		{"empty option", `{"questions":[{"question":"q","options":["a","  "]}]}`, "empty"},
	}
	for _, tc := range cases {
		if _, err := tool.Call(json.RawMessage(tc.args), ctx); err == nil {
			t.Fatalf("%s passed", tc.name)
		} else if !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: %q should contain %q", tc.name, err.Error(), tc.want)
		}
	}
}

func TestAskNeedsSessionAndCall(t *testing.T) {
	tool := NewAskTool()
	args := json.RawMessage(`{"questions":[{"question":"go on?","options":["yes","no"]}]}`)
	if _, err := tool.Call(args, nil); err == nil || !strings.Contains(err.Error(), "no session") {
		t.Fatalf("nil context should fail, got %v", err)
	}
	if _, err := tool.Call(args, &ToolCtx{Cancel: context.Background()}); err == nil || !strings.Contains(err.Error(), "no session") {
		t.Fatalf("empty ids should fail, got %v", err)
	}
}

func TestAskResolveRoundTrip(t *testing.T) {
	tool := NewAskTool()
	got := make(chan string, 1)
	go func() {
		out, err := tool.Call(
			json.RawMessage(`{"questions":[{"question":"Deploy?","options":["yes","no"]},{"question":"Region?"}]}`),
			testAskCtx(t, "sess-1", "c1"),
		)
		if err != nil {
			t.Errorf("call: %v", err)
			got <- ""
			return
		}
		got <- out
	}()
	waitAskPending(t, tool, "sess-1", "c1", true)
	// Unknown call ids fail so the UI can report a moved-on turn.
	if err := tool.Resolve("sess-1", "nope", nil); err == nil {
		t.Fatal("resolve of unknown call passed")
	}
	answers := []AskAnswer{{Question: "Deploy?", Answer: "yes"}, {Question: "Region?", Answer: "eu"}}
	if err := tool.Resolve("sess-1", "c1", answers); err != nil {
		t.Fatal(err)
	}
	out := <-got
	var decoded struct {
		Answers []AskAnswer `json:"answers"`
	}
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("result not JSON: %s", out)
	}
	if len(decoded.Answers) != 2 || decoded.Answers[0].Answer != "yes" || decoded.Answers[1].Answer != "eu" {
		t.Fatalf("got %s", out)
	}
	waitAskPending(t, tool, "sess-1", "c1", false)
}

func TestAskCancelStopsWaitingCall(t *testing.T) {
	tool := NewAskTool()
	done := make(chan error, 1)
	go func() {
		_, err := tool.Call(json.RawMessage(`{"questions":[{"question":"Continue?"}]}`), testAskCtx(t, "sess-2", "c9"))
		done <- err
	}()
	waitAskPending(t, tool, "sess-2", "c9", true)
	if !tool.Cancel("sess-2", "c9") {
		t.Fatal("cancel missed the pending call")
	}
	if err := <-done; err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("got %v", err)
	}
	if tool.Cancel("sess-2", "c9") {
		t.Fatal("second cancel should report nothing waiting")
	}
}

func TestAskRejectsEmptyAnswers(t *testing.T) {
	tool := NewAskTool()
	done := make(chan error, 1)
	go func() {
		_, err := tool.Call(json.RawMessage(`{"questions":[{"question":"A?"},{"question":"B?"}]}`), testAskCtx(t, "sess-3", "c1"))
		done <- err
	}()
	waitAskPending(t, tool, "sess-3", "c1", true)
	answers := []AskAnswer{{Question: "A?", Answer: "fine"}, {Question: "B?", Answer: "  "}}
	if err := tool.Resolve("sess-3", "c1", answers); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("got %v", err)
	}
	if err := tool.Resolve("sess-3", "c1", []AskAnswer{{Question: "A?", Answer: "x"}}); err == nil {
		t.Fatal("second resolve after drain should fail")
	}
}

func TestAskContextCancelInterrupts(t *testing.T) {
	tool := NewAskTool()
	ctx, cancel := context.WithCancel(context.Background())
	toolCtx := &ToolCtx{Workspace: t.TempDir(), SessionDir: t.TempDir(), Cancel: ctx, SessionID: "sess-4", CallID: "c1"}
	done := make(chan error, 1)
	go func() {
		_, err := tool.Call(json.RawMessage(`{"questions":[{"question":"Wait?"}]}`), toolCtx)
		done <- err
	}()
	waitAskPending(t, tool, "sess-4", "c1", true)
	cancel()
	if err := <-done; err == nil || !strings.Contains(err.Error(), "interrupted") {
		t.Fatalf("got %v", err)
	}
}

func TestAskDuplicateCallRejected(t *testing.T) {
	tool := NewAskTool()
	tool.mu.Lock()
	tool.pending[askKey("s", "c")] = &askPending{done: make(chan askOutcome, 1)}
	tool.mu.Unlock()
	_, err := tool.Call(
		json.RawMessage(`{"questions":[{"question":"q"}]}`),
		testAskCtx(t, "s", "c"),
	)
	if err == nil || !strings.Contains(err.Error(), "already waiting") {
		t.Fatalf("got %v", err)
	}
}

func TestAskRegistryStampsCallID(t *testing.T) {
	reg := NewRegistry()
	tool := NewAskTool()
	reg.Register(tool)
	done := make(chan error, 1)
	go func() {
		res := reg.Call(
			&ToolCall{ID: " stamped ", Name: "ask", Arguments: `{"questions":[{"question":"q"}]}`},
			&ToolCtx{Workspace: t.TempDir(), SessionDir: t.TempDir(), Cancel: context.Background(), SessionID: "sess-5"},
		)
		if res.IsError {
			done <- errors.New(res.Content)
			return
		}
		done <- nil
	}()
	waitAskPending(t, tool, "sess-5", " stamped ", true)
	if err := tool.Resolve("sess-5", " stamped ", []AskAnswer{{Question: "q", Answer: "a"}}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestAskMultipleSelection(t *testing.T) {
	qs, err := parseAskQuestions(json.RawMessage(`{"questions":[{"question":"Which?","options":["a","b","c"],"multiple":true},{"question":"One?","options":["x","y"]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !qs[0].Multiple || qs[1].Multiple {
		t.Fatalf("multiple flag lost: %+v", qs)
	}
	out, err := summarizeAsk(qs, []AskAnswer{{Selected: []string{"a", "c"}}, {Answer: "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"answer":"a, c"`) || !strings.Contains(out, `"selected":["a","c"]`) {
		t.Fatalf("out = %s", out)
	}
	if _, err := summarizeAsk(qs, []AskAnswer{{Selected: []string{"a"}}, {Selected: []string{"x", "y"}}}); err == nil {
		t.Fatal("single-choice question must reject several selections")
	}
	if _, err := summarizeAsk(qs, []AskAnswer{{Selected: []string{}}, {Answer: "x"}}); err == nil {
		t.Fatal("empty multi answer must fail")
	}
}
