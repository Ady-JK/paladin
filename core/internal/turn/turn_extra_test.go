package turn

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Ady-JK/paladin/core/internal/inference"
	"github.com/Ady-JK/paladin/core/internal/session"
	"github.com/Ady-JK/paladin/core/internal/tools"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

func testAgent(t *testing.T, provider inference.Provider, maxRounds uint32) (*Agent, transcript.Session, chan ChatChunk, chan struct{}) {
	t.Helper()
	dir := t.TempDir()
	sess := transcript.Session{
		Meta: transcript.Meta{SessionID: "00000000-0000-4000-8000-000000000001", Cwd: dir, CreatedAt: time.Now().UTC()},
		Dir:  dir,
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	reg := tools.NewRegistry()
	reg.Register(tools.ReadTool{})
	shell := tools.NewShellTool()
	reg.Register(shell)
	tx := make(chan ChatChunk, 128)
	done := make(chan struct{})
	toolDefs := []inference.ToolDefinition{{Name: "read", Description: "r"}}
	agent := NewAgent(sess, provider, reg, shell, ctx, cancel, "sys", maxRounds, nil, toolDefs, tx, done)
	return agent, sess, tx, done
}

func drainChunks(tx chan ChatChunk) []ChatChunk {
	var out []ChatChunk
	for {
		select {
		case c := <-tx:
			out = append(out, c)
		default:
			return out
		}
	}
}

func TestRunTurnRejectsEmptyMessage(t *testing.T) {
	agent, _, tx, done := testAgent(t, &mockProvider{}, 5)
	defer close(done)
	if _, err := agent.RunTurn("", false); err == nil {
		t.Fatal("empty message accepted")
	}
	_ = tx
}

func TestRunTurnStreamsTextAndPersists(t *testing.T) {
	prov := &mockProvider{streams: []streamScript{{
		chunks: []inference.Chunk{inference.TextChunk("hello")},
	}}}
	agent, sess, tx, done := testAgent(t, prov, 5)
	defer close(done)
	outcome, err := agent.RunTurn("hi", false)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Reply != "hello" || outcome.Stopped || outcome.NeedsContinue {
		t.Fatalf("got %+v", outcome)
	}
	chunks := drainChunks(tx)
	found := false
	for _, c := range chunks {
		if c.Delta == "hello" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no delta chunk: %+v", chunks)
	}
	// User + assistant persisted.
	history, err := sess.History()
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 || history[0].Role != transcript.RoleUser || history[1].Role != transcript.RoleAssistant {
		t.Fatalf("got %+v", history)
	}
}

func TestRunTurnEmptyStreamFallsBackToBuffered(t *testing.T) {
	prov := &mockProvider{
		streams:  []streamScript{{}},
		complete: inference.ToolResponse{Content: "buffered"},
	}
	agent, _, tx, done := testAgent(t, prov, 5)
	defer close(done)
	outcome, err := agent.RunTurn("hi", false)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Reply != "buffered" {
		t.Fatalf("got %+v", outcome)
	}
	_ = tx
}

func TestRunTurnToolRound(t *testing.T) {
	dir := t.TempDir()
	wsFile := dir + "/note.txt"
	writeFile := func(s string) {
		t.Helper()
		if err := writeTestFile(wsFile, s); err != nil {
			t.Fatal(err)
		}
	}
	writeFile("file content here\n")
	prov := &mockProvider{streams: []streamScript{
		{chunks: []inference.Chunk{{ToolCalls: []inference.ToolCall{{ID: "c1", Name: "read", Arguments: `{"path": "` + wsFile + `"}`}}}}},
		{chunks: []inference.Chunk{inference.TextChunk("saw it")}},
	}}
	sess := transcript.Session{
		Meta: transcript.Meta{SessionID: "00000000-0000-4000-8000-000000000002", Cwd: dir, CreatedAt: time.Now().UTC()},
		Dir:  dir,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reg := tools.NewRegistry()
	reg.Register(tools.ReadTool{})
	shell := tools.NewShellTool()
	reg.Register(shell)
	tx := make(chan ChatChunk, 128)
	done := make(chan struct{})
	defer close(done)
	toolDefs := []inference.ToolDefinition{{Name: "read", Description: "r"}}
	agent := NewAgent(sess, prov, reg, shell, ctx, cancel, "sys", 5, nil, toolDefs, tx, done)
	outcome, err := agent.RunTurn("read it", false)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Reply != "saw it" {
		t.Fatalf("got %+v", outcome)
	}
	chunks := drainChunks(tx)
	var toolChunks []*ToolActivity
	for i := range chunks {
		if chunks[i].Tool != nil {
			toolChunks = append(toolChunks, chunks[i].Tool)
		}
	}
	if len(toolChunks) < 2 {
		t.Fatalf("want running + done tool events, got %+v", chunks)
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

func TestRunTurnUnknownToolIsErrorResult(t *testing.T) {
	prov := &mockProvider{streams: []streamScript{
		{chunks: []inference.Chunk{{ToolCalls: []inference.ToolCall{{ID: "c9", Name: "nope", Arguments: "{}"}}}}},
		{chunks: []inference.Chunk{inference.TextChunk("after")}},
	}}
	agent, sess, tx, done := testAgent(t, prov, 5)
	defer close(done)
	outcome, err := agent.RunTurn("hi", false)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Reply != "after" {
		t.Fatalf("got %+v", outcome)
	}
	_ = tx
	history, err := sess.History()
	if err != nil {
		t.Fatal(err)
	}
	foundErr := false
	for _, m := range history {
		if m.Role == transcript.RoleTool && m.IsError {
			foundErr = true
		}
	}
	if !foundErr {
		t.Fatalf("no error tool row: %+v", history)
	}
}

func TestRunTurnProviderError(t *testing.T) {
	prov := &mockProvider{streamsErr: inference.RequestError("down")}
	agent, _, _, done := testAgent(t, prov, 5)
	defer close(done)
	if _, err := agent.RunTurn("hi", false); err == nil {
		t.Fatal("provider error swallowed")
	} else if !strings.Contains(err.Error(), "provider unavailable") {
		t.Fatalf("got %v", err)
	}
}

func TestRunTurnStopMidStream(t *testing.T) {
	prov := &mockProvider{streams: []streamScript{{
		chunks: []inference.Chunk{inference.TextChunk("partial")},
		err:    inference.CancelledError(),
	}}}
	agent, _, _, done := testAgent(t, prov, 5)
	defer close(done)
	agent.cancel()
	outcome, err := agent.RunTurn("hi", false)
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.Stopped {
		t.Fatalf("got %+v", outcome)
	}
}

func TestRunTurnNeedsContinueAtMaxRounds(t *testing.T) {
	// Stream always returns another tool call so rounds never settle.
	prov := &mockProvider{streams: []streamScript{
		{chunks: []inference.Chunk{{ToolCalls: []inference.ToolCall{{ID: "c1", Name: "nope", Arguments: "{}"}}}}},
		{chunks: []inference.Chunk{{ToolCalls: []inference.ToolCall{{ID: "c2", Name: "nope", Arguments: "{}"}}}}},
	}}
	agent, _, _, done := testAgent(t, prov, 1)
	defer close(done)
	outcome, err := agent.RunTurn("hi", false)
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.NeedsContinue {
		t.Fatalf("got %+v", outcome)
	}
}

func TestRunTurnResumeSkipsUserAppend(t *testing.T) {
	prov := &mockProvider{streams: []streamScript{{chunks: []inference.Chunk{inference.TextChunk("r")}}}}
	agent, sess, _, done := testAgent(t, prov, 5)
	defer close(done)
	if _, err := agent.RunTurn("", true); err != nil {
		t.Fatal(err)
	}
	history, err := sess.History()
	if err != nil {
		t.Fatal(err)
	}
	// Resume persists no user message: only the assistant reply.
	if len(history) != 1 || history[0].Role != transcript.RoleAssistant {
		t.Fatalf("got %+v", history)
	}
}

func TestModelMessagesIncludesCurrent(t *testing.T) {
	h := []transcript.Message{transcript.UserMessage("old")}
	with := LlmMessages(h, true)
	if len(with) != 1 || with[0].Content != "old" {
		t.Fatalf("got %+v", with)
	}
	agent, _, _, done := testAgent(t, &mockProvider{}, 5)
	defer close(done)
	msgs := agent.modelMessages("new", false)
	if len(msgs) != 1 || msgs[0].Content != "new" {
		t.Fatalf("got %+v", msgs)
	}
	resumed := agent.modelMessages("new", true)
	if len(resumed) != 0 {
		t.Fatalf("resume should not append: %+v", resumed)
	}
}

func TestLlmMessagesEdgeCases(t *testing.T) {
	now := time.Now().UTC()
	// Assistant blank without calls is dropped.
	msgs := LlmMessages([]transcript.Message{
		{Role: transcript.RoleAssistant, Content: "  ", CreatedAt: now},
	}, true)
	if len(msgs) != 0 {
		t.Fatalf("got %+v", msgs)
	}
	// Assistant blank WITH calls survives (tool path).
	stored := []transcript.ToolCall{{ID: "c1", Name: "read", Arguments: "{}"}}
	msgs = LlmMessages([]transcript.Message{
		{Role: transcript.RoleAssistant, Content: "", CreatedAt: now, ToolCalls: &stored},
	}, true)
	if len(msgs) != 1 || len(msgs[0].ToolCalls) != 1 {
		t.Fatalf("got %+v", msgs)
	}
	// Empty tool-call IDs are skipped.
	stored2 := []transcript.ToolCall{{ID: "", Name: "read", Arguments: "{}"}}
	msgs = LlmMessages([]transcript.Message{
		{Role: transcript.RoleAssistant, Content: "x", CreatedAt: now, ToolCalls: &stored2},
	}, true)
	if len(msgs) != 1 || len(msgs[0].ToolCalls) != 0 {
		t.Fatalf("got %+v", msgs)
	}
	// Notify tool rows demote to user context.
	nid, nname := "c1#notify", "shell"
	msgs = LlmMessages([]transcript.Message{
		{Role: transcript.RoleAssistant, CreatedAt: now, ToolCalls: &[]transcript.ToolCall{{ID: "c1", Name: "shell", Arguments: "{}"}}},
		{Role: transcript.RoleTool, Content: "bg done", CreatedAt: now, ToolCallID: &nid, Name: &nname},
	}, true)
	if len(msgs) != 2 || msgs[1].Role != "user" {
		t.Fatalf("got %+v", msgs)
	}
	// Nil history -> empty non-nil slice.
	if out := LlmMessages(nil, true); out == nil || len(out) != 0 {
		t.Fatalf("got %+v", out)
	}
}

func TestNotifyContentShape(t *testing.T) {
	n := noticeForTest()
	content := notifyContent(&n)
	var wire map[string]any
	if err := json.Unmarshal([]byte(content), &wire); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"notice", "job_id", "status", "exit_code", "output", "truncated"} {
		if _, ok := wire[k]; !ok {
			t.Fatalf("missing %q in %s", k, content)
		}
	}
}

func TestAppendNotifyShape(t *testing.T) {
	agent, _, _, done := testAgent(t, &mockProvider{}, 5)
	defer close(done)
	agent.appendNotify("content", "c1#notify", true)
	if len(agent.records) != 1 || !agent.records[0].IsNotify {
		t.Fatalf("got %+v", agent.records)
	}
	if len(agent.messages) != 1 || agent.messages[0].Role != "user" {
		t.Fatalf("got %+v", agent.messages)
	}
}

func TestRunPlainPathStreamsText(t *testing.T) {
	dir := t.TempDir()
	sess := transcript.Session{
		Meta: transcript.Meta{SessionID: "00000000-0000-4000-8000-000000000003", Cwd: dir, CreatedAt: time.Now().UTC()},
		Dir:  dir,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reg := tools.NewRegistry()
	shell := tools.NewShellTool()
	reg.Register(shell)
	tx := make(chan ChatChunk, 128)
	done := make(chan struct{})
	defer close(done)
	prov := &mockProvider{streams: []streamScript{{chunks: []inference.Chunk{inference.TextChunk("plain hello")}}}}
	agent := NewAgent(sess, prov, reg, shell, ctx, cancel, "sys", 5, nil, nil, tx, done)
	outcome, err := agent.RunTurn("hi", false)
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
	dir := t.TempDir()
	sess := transcript.Session{
		Meta: transcript.Meta{SessionID: "00000000-0000-4000-8000-000000000004", Cwd: dir, CreatedAt: time.Now().UTC()},
		Dir:  dir,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reg := tools.NewRegistry()
	shell := tools.NewShellTool()
	tx := make(chan ChatChunk, 128)
	done := make(chan struct{})
	defer close(done)
	prov := &mockProvider{
		streams:  []streamScript{{}},
		complete: inference.ToolResponse{Content: "buffered plain"},
	}
	agent := NewAgent(sess, prov, reg, shell, ctx, cancel, "sys", 5, nil, nil, tx, done)
	outcome, err := agent.RunTurn("hi", false)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Reply != "buffered plain" {
		t.Fatalf("got %+v", outcome)
	}
}

func TestRunTurnBackgroundNotifyRoundTrip(t *testing.T) {
	dir := t.TempDir()
	sess := transcript.Session{
		Meta: transcript.Meta{SessionID: "00000000-0000-4000-8000-000000000005", Cwd: dir, CreatedAt: time.Now().UTC()},
		Dir:  dir,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	shell := tools.NewShellTool()
	reg := tools.NewRegistry()
	reg.Register(shell)
	tx := make(chan ChatChunk, 256)
	done := make(chan struct{})
	defer close(done)
	callArgs := `{"cmd":"echo notify-bg","background":true,"notify":true}`
	prov := &mockProvider{streams: []streamScript{
		{chunks: []inference.Chunk{{ToolCalls: []inference.ToolCall{{ID: "c1", Name: "shell", Arguments: callArgs}}}}},
		{chunks: []inference.Chunk{inference.TextChunk("after")}},
	}}
	toolDefs := []inference.ToolDefinition{{Name: "shell", Description: "s"}}
	agent := NewAgent(sess, prov, reg, shell, ctx, cancel, "sys", 5, nil, toolDefs, tx, done)
	outcome, err := agent.RunTurn("run bg", false)
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
drain:
	for {
		select {
		case c := <-tx:
			if c.Tool != nil && c.Tool.ID == "c1#notify" {
				notifyEvents++
			}
		default:
			break drain
		}
	}
	if notifyEvents != 1 {
		t.Fatalf("want 1 notify tool event, got %d", notifyEvents)
	}
}

func TestStopRequestedAndNeedsContinue(t *testing.T) {
	agent, _, _, done := testAgent(t, &mockProvider{}, 3)
	defer close(done)
	if agent.stopRequested() {
		t.Fatal("stop before cancel")
	}
	if agent.needsContinue() {
		t.Fatal("round 0 needs continue")
	}
	agent.round = 3
	if !agent.needsContinue() {
		t.Fatal("round 3 should need continue")
	}
	agent.cancel()
	if !agent.stopRequested() {
		t.Fatal("stop after cancel")
	}
}

func TestNewApiErrorAndChatChunkFields(t *testing.T) {
	e := NewApiError("internal", "boom")
	if e.Code != "internal" || e.Message != "boom" {
		t.Fatalf("got %+v", e)
	}
	c := ChatChunk{Delta: "d", Done: true, NeedsContinue: true, SessionID: "s", Stopped: true}
	if !c.Done || !c.NeedsContinue || !c.Stopped {
		t.Fatalf("got %+v", c)
	}
}
