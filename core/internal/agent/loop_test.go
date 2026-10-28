package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/Ady-JK/paladin/core/internal/inference"
	"github.com/Ady-JK/paladin/core/internal/session"
	"github.com/Ady-JK/paladin/core/internal/tools"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

func TestRunEmptyStreamFallsBackToBuffered(t *testing.T) {
	prov := &mockProvider{
		streams:  []streamScript{{}},
		complete: inference.ToolResponse{Content: "buffered"},
	}
	a, _, _, done := testAgent(t, prov, 5)
	defer close(done)
	outcome, err := a.Run()
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Reply != "buffered" {
		t.Fatalf("got %+v", outcome)
	}
}

func TestRunPersistentEmptyFailsLoudly(t *testing.T) {
	// A stream (and its buffered fallback) that never yields text or
	// calls must not end the turn as a silent READY: three strikes
	// with zero progress fails so the UI shows an error, not a fake
	// done that forces another nudge.
	prov := &mockProvider{
		streams: []streamScript{{}, {}, {}, {}},
	}
	a, _, _, done := testAgent(t, prov, 5)
	defer close(done)
	_, err := a.Run()
	if err == nil {
		t.Fatal("persistent empty stream ended the turn silently")
	}
	if terr, ok := err.(turnFailed); !ok || !strings.Contains(terr.msg, "empty response") {
		t.Fatalf("got %T %v", err, err)
	}
}

func TestRunEmptyRoundRetriesThenRecovers(t *testing.T) {
	// One dead round followed by a live one: the turn continues and
	// delivers the later text instead of stopping early.
	prov := &mockProvider{
		streams: []streamScript{{}, {chunks: []inference.Chunk{inference.TextChunk("back")}}},
	}
	a, _, _, done := testAgent(t, prov, 5)
	defer close(done)
	outcome, err := a.Run()
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Reply != "back" {
		t.Fatalf("got %+v", outcome)
	}
}

func TestRunRetryableErrorRetriesThenRecovers(t *testing.T) {
	old := retryUnit
	retryUnit = time.Millisecond
	defer func() { retryUnit = old }()
	prov := &mockProvider{
		streams: []streamScript{
			{err: inference.RequestError("boom: stream: status 503")},
			{chunks: []inference.Chunk{inference.TextChunk("back")}},
		},
	}
	a, _, _, done := testAgent(t, prov, 5)
	defer close(done)
	outcome, err := a.Run()
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Reply != "back" {
		t.Fatalf("got %+v", outcome)
	}
	if prov.calls != 2 {
		t.Fatalf("expected a retry round, got %d stream calls", prov.calls)
	}
}

func TestRunNonRetryableErrorFailsFast(t *testing.T) {
	prov := &mockProvider{
		streams: []streamScript{{err: inference.RequestError("boom: stream: status 401 unauthorized")}},
	}
	a, _, _, done := testAgent(t, prov, 5)
	defer close(done)
	start := time.Now()
	if _, err := a.Run(); err == nil {
		t.Fatal("auth error retried instead of failing")
	} else if !strings.Contains(err.Error(), "provider unavailable") {
		t.Fatalf("got %v", err)
	}
	if prov.calls != 1 {
		t.Fatalf("expected no retry, got %d stream calls", prov.calls)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("non-retryable error slept before failing")
	}
}

func TestRunRetryBudgetExhausts(t *testing.T) {
	old := retryUnit
	retryUnit = time.Millisecond
	defer func() { retryUnit = old }()
	prov := &mockProvider{
		streams: []streamScript{
			{err: inference.RequestError("boom: stream: status 503")},
			{err: inference.RequestError("boom: stream: status 503")},
			{err: inference.RequestError("boom: stream: status 503")},
			{err: inference.RequestError("boom: stream: status 503")},
			{err: inference.RequestError("boom: stream: status 503")},
		},
	}
	a, _, _, done := testAgent(t, prov, 10)
	defer close(done)
	if _, err := a.Run(); err == nil {
		t.Fatal("persistent 503 ended the turn silently")
	}
	if prov.calls != 4 {
		t.Fatalf("expected 1 + 3 retries, got %d stream calls", prov.calls)
	}
}

func TestRunToolRound(t *testing.T) {
	dir := t.TempDir()
	wsFile := dir + "/note.txt"
	if err := writeTestFile(wsFile, "file content here\n"); err != nil {
		t.Fatal(err)
	}
	prov := &mockProvider{streams: []streamScript{
		{chunks: []inference.Chunk{{ToolCalls: []inference.ToolCall{{ID: "c1", Name: "read", Arguments: `{"path": "` + wsFile + `"}`}}}}},
		{chunks: []inference.Chunk{inference.TextChunk("saw it")}},
	}}
	sess := testSession(t, "00000000-0000-4000-8000-000000000002")
	a, tx, done := newAgentForTest(t, prov, sess, 5, "read it", false, true)
	defer close(done)
	outcome, err := a.Run()
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Reply != "saw it" {
		t.Fatalf("got %+v", outcome)
	}
	var toolChunks []*ToolActivity
	for _, c := range drainChunks(tx) {
		if c.Tool != nil {
			toolChunks = append(toolChunks, c.Tool)
		}
	}
	if len(toolChunks) < 2 {
		t.Fatalf("want running + done tool events, got %+v", toolChunks)
	}
	if toolChunks[0].Status != session.ToolRunning || toolChunks[1].Status != session.ToolOK {
		t.Fatalf("got %+v", toolChunks)
	}
	if !strings.Contains(toolChunks[1].Result, "file content here") {
		t.Fatalf("tool result not wired: %+v", toolChunks[1])
	}
	history, err := sess.History()
	if err != nil {
		t.Fatal(err)
	}
	// user, assistant-with-call, tool result, assistant text.
	if len(history) != 4 {
		t.Fatalf("got %d: %+v", len(history), history)
	}
}

func TestRunUsesStableSessionIDAcrossRounds(t *testing.T) {
	dir := t.TempDir()
	wsFile := dir + "/note.txt"
	if err := writeTestFile(wsFile, "cached prefix\n"); err != nil {
		t.Fatal(err)
	}
	prov := &mockProvider{streams: []streamScript{
		{chunks: []inference.Chunk{{ToolCalls: []inference.ToolCall{{ID: "c1", Name: "read", Arguments: `{"path": "` + wsFile + `"}`}}}}},
		{chunks: []inference.Chunk{inference.TextChunk("done")}},
	}}
	const sid = "00000000-0000-4000-8000-0000000000aa"
	sess := testSession(t, sid)
	a, _, done := newAgentForTest(t, prov, sess, 5, "read it", false, true)
	defer close(done)
	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	if len(prov.sessionIDs) < 2 {
		t.Fatalf("expected multiple rounds, got %d", len(prov.sessionIDs))
	}
	for i, got := range prov.sessionIDs {
		if got != sid {
			t.Fatalf("round %d session %q, want %q", i, got, sid)
		}
	}
}

func TestRunProviderError(t *testing.T) {
	prov := &mockProvider{streamsErr: inference.RequestError("down")}
	a, _, _, done := testAgent(t, prov, 5)
	defer close(done)
	if _, err := a.Run(); err == nil {
		t.Fatal("provider error swallowed")
	} else if !strings.Contains(err.Error(), "provider unavailable") {
		t.Fatalf("got %v", err)
	}
}

func TestRunStopMidStream(t *testing.T) {
	prov := &mockProvider{streams: []streamScript{{
		chunks: []inference.Chunk{inference.TextChunk("partial")},
		err:    inference.CancelledError(),
	}}}
	a, sess, _, done := testAgent(t, prov, 5)
	defer close(done)
	outcome, err := a.Run()
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.Stopped {
		t.Fatalf("got %+v", outcome)
	}
	// The partial text of a stopped turn is still persisted once.
	history, err := sess.History()
	if err != nil {
		t.Fatal(err)
	}
	var partial bool
	for _, m := range history {
		if m.Content == "partial" {
			partial = true
		}
	}
	if !partial {
		t.Fatalf("partial text lost: %+v", history)
	}
}

func TestRunNeedsContinueAtMaxRounds(t *testing.T) {
	// Stream always returns another tool call so rounds never settle.
	prov := &mockProvider{streams: []streamScript{
		{chunks: []inference.Chunk{{ToolCalls: []inference.ToolCall{{ID: "c1", Name: "nope", Arguments: "{}"}}}}},
		{chunks: []inference.Chunk{{ToolCalls: []inference.ToolCall{{ID: "c2", Name: "nope", Arguments: "{}"}}}}},
	}}
	a, _, _, done := testAgent(t, prov, 1)
	defer close(done)
	outcome, err := a.Run()
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.NeedsContinue {
		t.Fatalf("got %+v", outcome)
	}
}

func TestRunPlainPathStreamsText(t *testing.T) {
	sess := testSession(t, "00000000-0000-4000-8000-000000000003")
	prov := &mockProvider{streams: []streamScript{{chunks: []inference.Chunk{inference.TextChunk("plain hello")}}}}
	a, _, done := newAgentForTest(t, prov, sess, 5, "hi", false, false)
	defer close(done)
	outcome, err := a.Run()
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Reply != "plain hello" {
		t.Fatalf("got %+v", outcome)
	}
	history, err := sess.History()
	if err != nil {
		t.Fatal(err)
	}
	// Plain path persists user + assistant.
	if len(history) != 2 || history[1].Role != transcript.RoleAssistant {
		t.Fatalf("got %+v", history)
	}
}

func TestRunPlainBufferedFallback(t *testing.T) {
	sess := testSession(t, "00000000-0000-4000-8000-000000000004")
	prov := &mockProvider{
		streams:  []streamScript{{}},
		complete: inference.ToolResponse{Content: "buffered plain"},
	}
	a, _, done := newAgentForTest(t, prov, sess, 5, "hi", false, false)
	defer close(done)
	outcome, err := a.Run()
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Reply != "buffered plain" {
		t.Fatalf("got %+v", outcome)
	}
}

func TestRunPlainProviderError(t *testing.T) {
	sess := testSession(t, "00000000-0000-4000-8000-000000000011")
	prov := &mockProvider{streamsErr: inference.RequestError("down")}
	a, _, done := newAgentForTest(t, prov, sess, 5, "hi", false, false)
	defer close(done)
	if _, err := a.Run(); err == nil || !strings.Contains(err.Error(), "provider unavailable") {
		t.Fatalf("got %v", err)
	}
}

func TestRunBackgroundNotifyRoundTrip(t *testing.T) {
	sess := testSession(t, "00000000-0000-4000-8000-000000000005")
	callArgs := `{"cmd":"echo notify-bg","background":true,"notify":true}`
	prov := &mockProvider{streams: []streamScript{
		{chunks: []inference.Chunk{{ToolCalls: []inference.ToolCall{{ID: "c1", Name: "shell", Arguments: callArgs}}}}},
		{chunks: []inference.Chunk{inference.TextChunk("after")}},
	}}
	a, tx, done := newAgentForTest(t, prov, sess, 5, "run bg", false, true)
	defer close(done)
	outcome, err := a.Run()
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Reply != "after" {
		t.Fatalf("got %+v", outcome)
	}
	history, err := sess.History()
	if err != nil {
		t.Fatal(err)
	}
	var sawNotify, sawShellResult bool
	for _, m := range history {
		if m.IsNotify {
			sawNotify = true
			if m.ToolCallID == nil || *m.ToolCallID != "c1#notify" {
				t.Fatalf("notify id: %+v", m)
			}
		}
		if m.Role == transcript.RoleTool && !m.IsNotify {
			sawShellResult = true
		}
	}
	if !sawShellResult || !sawNotify {
		t.Fatalf("shell=%v notify=%v in %+v", sawShellResult, sawNotify, history)
	}
	// Notify rows are surfaced as tool activity chunks.
	var notifyEvents int
	for _, c := range drainChunks(tx) {
		if c.Tool != nil && c.Tool.ID == "c1#notify" {
			notifyEvents++
		}
	}
	if notifyEvents != 1 {
		t.Fatalf("want 1 notify tool event, got %d", notifyEvents)
	}
}

func TestArmNotifyOnlyForBackgroundNotifyJobs(t *testing.T) {
	a, _, _, done := testAgent(t, &mockProvider{}, 5)
	defer close(done)
	cases := []struct {
		name string
		args string
	}{
		{"foreground", `{"cmd":"true"}`},
		{"no notify", `{"cmd":"true","background":true}`},
		{"not run", `{"action":"status","background":true,"notify":true}`},
		{"bad json", `{`},
		{"empty receipt", `{}`},
	}
	for _, tc := range cases {
		call := &inference.ToolCall{ID: "c1", Name: "shell", Arguments: tc.args}
		result := tools.ToolResult{Content: `{"job_id":"job-1","status":"running"}`}
		a.armNotify(call, &result)
		if got := a.shell.PollNotify(a.session.Meta.SessionID); len(got) != 0 {
			t.Fatalf("%s armed unexpectedly: %+v", tc.name, got)
		}
	}
}
