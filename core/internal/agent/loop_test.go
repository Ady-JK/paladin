package agent

import (
	"encoding/json"
	"fmt"
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
	a, _, _, done := testAgent(t, prov)
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
	a, _, _, done := testAgent(t, prov)
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
	a, _, _, done := testAgent(t, prov)
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
	a, _, _, done := testAgent(t, prov)
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

// TestRunRetriesTruncatedStream pins the deepseek/opencode failure: a
// stream the SDK cut off mid-frame ("unexpected end of JSON input") is
// a transport blip, so the round retries instead of ending the turn.
func TestRunRetriesTruncatedStream(t *testing.T) {
	old := retryUnit
	retryUnit = time.Millisecond
	defer func() { retryUnit = old }()
	prov := &mockProvider{
		streams: []streamScript{
			{err: inference.RequestError("Opencode Go: stream: unexpected end of JSON input")},
			{chunks: []inference.Chunk{inference.TextChunk("recovered")}},
		},
	}
	a, _, _, done := testAgent(t, prov)
	defer close(done)
	outcome, err := a.Run()
	if err != nil {
		t.Fatalf("a truncated stream must retry: %v", err)
	}
	if outcome.Reply != "recovered" || prov.calls != 2 {
		t.Fatalf("reply=%q calls=%d", outcome.Reply, prov.calls)
	}
}

// TestRunKeepsPartialTextOnStreamFailure pins the other half: when the
// round already streamed text and the failure is not recoverable, that
// text stays in the transcript instead of vanishing with the error.
func TestRunKeepsPartialTextOnStreamFailure(t *testing.T) {
	prov := &mockProvider{streams: []streamScript{{
		chunks: []inference.Chunk{inference.TextChunk("half an answer")},
		err:    inference.RequestError("boom: stream: status 401 unauthorized"),
	}}}
	a, sess, _, done := testAgent(t, prov)
	defer close(done)
	_, err := a.Run()
	if err == nil {
		t.Fatal("want the turn to fail")
	}
	h, _ := sess.History()
	for _, m := range h {
		if m.Role == transcript.RoleAssistant && strings.Contains(m.Content, "half an answer") {
			return
		}
	}
	t.Fatalf("partial text was dropped: %+v", h)
}

func TestRunNonRetryableErrorFailsFast(t *testing.T) {
	prov := &mockProvider{
		streams: []streamScript{{err: inference.RequestError("boom: stream: status 401 unauthorized")}},
	}
	a, _, _, done := testAgent(t, prov)
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
	a, tx, done := newAgentForTest(t, prov, sess, "read it", false, true)
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
	a, _, done := newAgentForTest(t, prov, sess, "read it", false, true)
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
	a, _, _, done := testAgent(t, prov)
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
	a, sess, _, done := testAgent(t, prov)
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

func TestLoopHasNoRoundCap(t *testing.T) {
	// Far past the old 100-round default: only the model ends the turn.
	const rounds = 150
	var streams []streamScript
	for i := 0; i < rounds; i++ {
		args := fmt.Sprintf(`{"path":"f%d.txt"}`, i)
		streams = append(streams, streamScript{chunks: []inference.Chunk{{ToolCalls: []inference.ToolCall{{ID: fmt.Sprintf("c%d", i), Name: "read", Arguments: args}}}}})
	}
	streams = append(streams, streamScript{chunks: []inference.Chunk{inference.TextChunk("all done")}})
	prov := &mockProvider{streams: streams}
	a, _, tx, done := testAgent(t, prov)
	defer close(done)
	go func() {
		for range tx {
		}
	}()
	outcome, err := a.Run()
	close(tx)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Reply != "all done" || prov.calls != rounds+1 {
		t.Fatalf("outcome %+v after %d calls", outcome, prov.calls)
	}
}

func TestRunPlainPathStreamsText(t *testing.T) {
	sess := testSession(t, "00000000-0000-4000-8000-000000000003")
	prov := &mockProvider{streams: []streamScript{{chunks: []inference.Chunk{inference.TextChunk("plain hello")}}}}
	a, _, done := newAgentForTest(t, prov, sess, "hi", false, false)
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
	a, _, done := newAgentForTest(t, prov, sess, "hi", false, false)
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
	a, _, done := newAgentForTest(t, prov, sess, "hi", false, false)
	defer close(done)
	if _, err := a.Run(); err == nil || !strings.Contains(err.Error(), "provider unavailable") {
		t.Fatalf("got %v", err)
	}
}

func TestRunBackgroundNotifyRoundTrip(t *testing.T) {
	// This test exercises job notification, not the user's shell startup files.
	t.Setenv("SHELL", "/bin/sh")
	sess := testSession(t, "00000000-0000-4000-8000-000000000005")
	callArgs := `{"cmd":"echo notify-bg","background":true,"notify":true}`
	prov := &mockProvider{streams: []streamScript{
		{chunks: []inference.Chunk{{ToolCalls: []inference.ToolCall{{ID: "c1", Name: "shell", Arguments: callArgs}}}}},
		{chunks: []inference.Chunk{inference.TextChunk("after")}},
	}}
	// The completion notice is delivered at the end of the round after
	// the job finishes; give the short echo command time to exit.
	prov.afterChunks = func(idx int) {
		if idx == 1 {
			time.Sleep(150 * time.Millisecond)
		}
	}
	a, tx, done := newAgentForTest(t, prov, sess, "run bg", false, true)
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

// TestTurnStatsTimesModelCallsOnly pins the token rate's denominator: the
// turn's genMs counts the model calls, not the wall time a tool spent.
// A 40ms model reply inside a turn padded by 120ms of tool time must
// still report roughly the model's own time, or the rate would measure
// turn throughput instead of generation speed.
func TestTurnStatsTimesModelCallsOnly(t *testing.T) {
	sess := testSession(t, "00000000-0000-4000-8000-000000000077")
	prov := &mockProvider{
		streams: []streamScript{
			{chunks: []inference.Chunk{{ToolCalls: []inference.ToolCall{
				{ID: "c1", Name: "shell", Arguments: `{"cmd":"echo slow"}`}}}}},
			{chunks: []inference.Chunk{inference.TextChunk("done")}},
		},
		afterChunks: func(idx int) {
			if idx == 0 {
				time.Sleep(40 * time.Millisecond)
			}
		},
	}
	a, _, done := newAgentForTest(t, prov, sess, "time it", false, true)
	defer close(done)
	// A tool that burns wall time without touching the model clock.
	a.tools.Register(slowProbe{})
	outcome, err := a.Run()
	if err != nil {
		t.Fatal(err)
	}
	stats := outcome.Stats
	if stats == nil {
		t.Fatal("no stats recorded")
	}
	if stats.GenMs <= 0 {
		t.Fatalf("genMs not recorded: %+v", stats)
	}
	// The model slept 40ms; the turn also ran the slow tool twice over.
	if stats.GenMs >= stats.DurationMs {
		t.Fatalf("genMs should be a fraction of the turn: %+v", stats)
	}
	if stats.GenMs < 30 || stats.GenMs > 110 {
		t.Fatalf("genMs %d does not reflect the model's 40ms", stats.GenMs)
	}
}

// slowProbe is a tool that takes wall time and no model time.
type slowProbe struct{}

func (p slowProbe) Name() string { return "shell" }

func (p slowProbe) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{Name: "shell", Parameters: map[string]any{"type": "object"}}
}

func (p slowProbe) Call(_ json.RawMessage, _ *tools.ToolCtx) (string, error) {
	time.Sleep(120 * time.Millisecond)
	return `{"status":"ok","exit_code":0,"output":"slow"}`, nil
}
