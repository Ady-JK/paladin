package agent

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Ady-JK/paladin/core/internal/config"
	"github.com/Ady-JK/paladin/core/internal/inference"
	"github.com/Ady-JK/paladin/core/internal/tools"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

// mockProvider scripts streaming + buffered responses per round.
type mockProvider struct {
	name        string
	streams     []streamScript
	complete    inference.ToolResponse
	completeErr error
	streamsErr  error
	calls       int
	sessionIDs  []string
	// streamMsgs / completeMsgs record what each call was sent.
	streamMsgs   [][]inference.LlmMessage
	completeMsgs [][]inference.LlmMessage
	completeSys  []string
	streamDefs   [][]inference.ToolDefinition
	// afterChunks runs once a stream's scripted chunks are delivered.
	afterChunks func(idx int)
}

type streamScript struct {
	chunks []inference.Chunk
	err    error
}

func (m *mockProvider) Name() string                                 { return m.name }
func (m *mockProvider) ApiType() config.ApiType                      { return config.ApiTypeOpenai }
func (m *mockProvider) Model() string                                { return "mock" }
func (m *mockProvider) ListModels(context.Context) ([]string, error) { return nil, nil }

func (m *mockProvider) CompleteTools(ctx context.Context, system string, messages []inference.LlmMessage, defs []inference.ToolDefinition, sessionID string) (inference.ToolResponse, error) {
	m.sessionIDs = append(m.sessionIDs, sessionID)
	m.completeMsgs = append(m.completeMsgs, append([]inference.LlmMessage(nil), messages...))
	m.completeSys = append(m.completeSys, system)
	return m.complete, m.completeErr
}

func (m *mockProvider) StreamTools(ctx context.Context, system string, messages []inference.LlmMessage, defs []inference.ToolDefinition, sessionID string, tx chan<- inference.Chunk, done <-chan struct{}) error {
	m.sessionIDs = append(m.sessionIDs, sessionID)
	m.streamMsgs = append(m.streamMsgs, append([]inference.LlmMessage(nil), messages...))
	m.streamDefs = append(m.streamDefs, defs)
	idx := m.calls
	m.calls++
	if m.streamsErr != nil {
		return m.streamsErr
	}
	if idx < len(m.streams) {
		for _, c := range m.streams[idx].chunks {
			select {
			case tx <- c:
			case <-done:
				return inference.CancelledError()
			case <-ctx.Done():
				return inference.CancelledError()
			}
		}
		if m.afterChunks != nil {
			m.afterChunks(idx)
		}
		return m.streams[idx].err
	}
	return nil
}

func writeTestFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

func noticeForTest() tools.NotifyResult {
	code := 0
	return tools.NotifyResult{
		CallID: "c1", JobID: "job-1", Status: tools.JobDone,
		ExitCode: &code, Output: "out", Truncated: false, IsError: false,
	}
}

func testSession(t *testing.T, id string) transcript.Session {
	t.Helper()
	dir := t.TempDir()
	return transcript.Session{
		Meta: transcript.Meta{SessionID: id, Cwd: dir, CreatedAt: time.Now().UTC()},
		Dir:  dir,
	}
}

func newAgentForTest(t *testing.T, provider inference.Provider, sess transcript.Session, message string, resume bool, withTools bool) (*Agent, chan ChatChunk, chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	reg := tools.NewRegistry()
	shell := tools.NewShellTool()
	reg.Register(shell)
	var defs []inference.ToolDefinition
	if withTools {
		reg.Register(tools.ReadTool{})
		defs = []inference.ToolDefinition{{Name: "read", Description: "r"}, {Name: "shell", Description: "s"}}
	}
	tx := make(chan ChatChunk, 256)
	done := make(chan struct{})
	a := newAgent(runConfig{
		session: sess, provider: provider, tools: reg, shell: shell,
		toolDefs: defs,
		ctx:      ctx, cancel: cancel, system: "sys",
		tx: tx, done: done, message: message, resume: resume,
	})
	return a, tx, done
}

// testAgent builds a tool-capable agent with read + shell registered.
func testAgent(t *testing.T, provider inference.Provider) (*Agent, transcript.Session, chan ChatChunk, chan struct{}) {
	t.Helper()
	sess := testSession(t, "00000000-0000-4000-8000-000000000001")
	a, tx, done := newAgentForTest(t, provider, sess, "hi", false, true)
	return a, sess, tx, done
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

func TestRunStreamsTextAndPersists(t *testing.T) {
	prov := &mockProvider{streams: []streamScript{{
		chunks: []inference.Chunk{inference.TextChunk("hello")},
	}}}
	a, sess, tx, done := testAgent(t, prov)
	defer close(done)
	outcome, err := a.Run()
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Reply != "hello" || outcome.Stopped {
		t.Fatalf("got %+v", outcome)
	}
	if outcome.SessionID != sess.Meta.SessionID {
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

func TestRunReplaysHistoryIntoModel(t *testing.T) {
	prov := &mockProvider{streams: []streamScript{{chunks: []inference.Chunk{inference.TextChunk("ok")}}}}
	sess := testSession(t, "00000000-0000-4000-8000-000000000010")
	if err := sess.Append(transcript.RoleUser, "earlier"); err != nil {
		t.Fatal(err)
	}
	a, _, done := newAgentForTest(t, prov, sess, "hi", false, true)
	defer close(done)
	history, err := sess.History()
	if err != nil {
		t.Fatal(err)
	}
	a.history = history
	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	// Persisted in order: old user, new user, new assistant.
	got, err := sess.History()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Content != "earlier" || got[1].Content != "hi" {
		t.Fatalf("got %+v", got)
	}
}

func TestRunResumeSkipsUserAppend(t *testing.T) {
	prov := &mockProvider{streams: []streamScript{{chunks: []inference.Chunk{inference.TextChunk("r")}}}}
	sess := testSession(t, "00000000-0000-4000-8000-000000000001")
	a, _, done := newAgentForTest(t, prov, sess, "", true, true)
	defer close(done)
	if _, err := a.Run(); err != nil {
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

func TestRunUnknownToolIsErrorResult(t *testing.T) {
	prov := &mockProvider{streams: []streamScript{
		{chunks: []inference.Chunk{{ToolCalls: []inference.ToolCall{{ID: "c9", Name: "nope", Arguments: "{}"}}}}},
		{chunks: []inference.Chunk{inference.TextChunk("after")}},
	}}
	a, sess, _, done := testAgent(t, prov)
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

func TestAppendNotifyShape(t *testing.T) {
	a, _, _, done := testAgent(t, &mockProvider{})
	defer close(done)
	a.appendNotify("content", "c1#notify", true)
	if len(a.records) != 1 || !a.records[0].IsNotify {
		t.Fatalf("got %+v", a.records)
	}
	if len(a.messages) != 1 || a.messages[0].Role != "user" {
		t.Fatalf("got %+v", a.messages)
	}
	if a.records[0].ToolCallID == nil || *a.records[0].ToolCallID != "c1#notify" {
		t.Fatalf("got %+v", a.records[0])
	}
}

func TestAppendAssistantAndToolResult(t *testing.T) {
	a, _, _, done := testAgent(t, &mockProvider{})
	defer close(done)
	calls := []inference.ToolCall{{ID: "c1", Name: "read", Arguments: "{}", Signature: "sig-1"}}
	a.appendAssistant("text", calls)
	call := &inference.ToolCall{ID: "c1", Name: "read", Arguments: "{}"}
	a.appendToolResult(call, &tools.ToolResult{CallID: "c1", Name: "read", Content: "out"})
	if len(a.messages) != 2 || len(a.messages[0].ToolCalls) != 1 {
		t.Fatalf("got %+v", a.messages)
	}
	if a.messages[0].ToolCalls[0].Signature != "sig-1" {
		t.Fatalf("signature not kept in memory: %+v", a.messages[0].ToolCalls[0])
	}
	if a.records[0].ToolCalls == nil || (*a.records[0].ToolCalls)[0].Signature != "sig-1" {
		t.Fatalf("signature not persisted: %+v", a.records[0].ToolCalls)
	}
	if a.messages[1].Role != "tool" || a.messages[1].Content != "out" {
		t.Fatalf("got %+v", a.messages[1])
	}
	if len(a.records) != 2 || a.records[0].ToolCalls == nil || a.records[1].IsError {
		t.Fatalf("got %+v", a.records)
	}
	// A nil result still records an empty tool row.
	a.appendToolResult(call, nil)
	if len(a.records) != 3 || a.records[2].Content != "" {
		t.Fatalf("got %+v", a.records)
	}
}

func TestStopRequested(t *testing.T) {
	a, _, _, done := testAgent(t, &mockProvider{})
	defer close(done)
	if a.stopRequested() {
		t.Fatal("stop before cancel")
	}
	a.cancel()
	if !a.stopRequested() {
		t.Fatal("stop after cancel")
	}
}

func TestNewApiErrorAndChatChunkFields(t *testing.T) {
	e := NewApiError("internal", "boom")
	if e.Code != "internal" || e.Message != "boom" {
		t.Fatalf("got %+v", e)
	}
	c := ChatChunk{Delta: "d", Done: true, SessionID: "s", Stopped: true}
	if !c.Done || !c.Stopped {
		t.Fatalf("got %+v", c)
	}
}

func TestEmitStopsWhenClientLeaves(t *testing.T) {
	a, _, _, done := testAgent(t, &mockProvider{})
	// Fill the stream buffer so only the done channel can make progress.
	for i := 0; i < cap(a.tx); i++ {
		a.tx <- ChatChunk{}
	}
	close(done)
	if err := a.emit(ChatChunk{Delta: "x"}); err == nil {
		t.Fatal("emit to a closed done channel should stop")
	}
}

func TestPersistFailureSurfaces(t *testing.T) {
	// A session whose directory does not exist cannot persist, and the
	// turn must report that instead of silently dropping the transcript.
	sess := transcript.Session{
		Meta: transcript.Meta{SessionID: "00000000-0000-4000-8000-000000000099", Cwd: t.TempDir(), CreatedAt: time.Now().UTC()},
		Dir:  "/nonexistent/paladin/session",
	}
	prov := &mockProvider{streams: []streamScript{{chunks: []inference.Chunk{inference.TextChunk("x")}}}}
	a, _, done := newAgentForTest(t, prov, sess, "hi", false, true)
	defer close(done)
	if _, err := a.Run(); err == nil {
		t.Fatal("persist failure swallowed")
	}
}
