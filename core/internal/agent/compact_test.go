package agent

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Ady-JK/paladin/core/internal/inference"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

func overflowErr() error {
	return &inference.ProviderError{Kind: "request", Status: 400, Message: "prompt is too long: 210000 tokens > 200000 maximum"}
}

// seedHistory writes n user/assistant exchanges of ~size chars each to
// the session and loads them into the agent, as Template.Build would.
func seedHistory(t *testing.T, a *Agent, sess transcript.Session, n, size int) {
	t.Helper()
	for i := 0; i < n; i++ {
		pad := strings.Repeat("x", size)
		if err := sess.Append(transcript.RoleUser, fmt.Sprintf("old-%d %s", i, pad)); err != nil {
			t.Fatal(err)
		}
		if err := sess.Append(transcript.RoleAssistant, fmt.Sprintf("reply-%d %s", i, pad)); err != nil {
			t.Fatal(err)
		}
	}
	h, err := sess.History()
	if err != nil {
		t.Fatal(err)
	}
	a.history = h
}

func compactionChunks(chunks []ChatChunk) []CompactionStatus {
	var out []CompactionStatus
	for _, c := range chunks {
		if c.Compaction != nil {
			out = append(out, *c.Compaction)
		}
	}
	return out
}

func TestLlmMessagesStartFromLastCompaction(t *testing.T) {
	now := time.Now().UTC()
	calls := []transcript.ToolCall{{ID: "c1", Name: "read", Arguments: `{}`}}
	id := "c1"
	name := "read"
	history := []transcript.Message{
		{Role: transcript.RoleUser, Content: "old", CreatedAt: now},
		{Role: transcript.RoleAssistant, Content: "old reply", CreatedAt: now},
		{Role: transcript.RoleSystem, Content: "S1", CreatedAt: now, Compaction: &transcript.Compaction{Reason: "auto"}},
		{Role: transcript.RoleUser, Content: "x", CreatedAt: now, Replay: true},
		{Role: transcript.RoleSystem, Content: "S2", CreatedAt: now, Compaction: &transcript.Compaction{Reason: "auto"}},
		{Role: transcript.RoleUser, Content: "task", CreatedAt: now, Replay: true},
		{Role: transcript.RoleAssistant, CreatedAt: now, ToolCalls: &calls, Replay: true},
		{Role: transcript.RoleTool, Content: "r", CreatedAt: now, ToolCallID: &id, Name: &name, Replay: true},
		{Role: transcript.RoleAssistant, Content: "after", CreatedAt: now},
	}
	got := LlmMessages(history, true)
	if len(got) != 5 {
		t.Fatalf("got %d: %+v", len(got), got)
	}
	if got[0].Role != "user" || got[0].Content != compactionUserContent("S2") {
		t.Fatalf("summary head %+v", got[0])
	}
	if got[1].Content != "task" || len(got[2].ToolCalls) != 1 || got[3].Role != "tool" || got[4].Content != "after" {
		t.Fatalf("tail %+v", got)
	}
}

func TestSplitForCompactionNeverSplitsToolPairs(t *testing.T) {
	c1, c2 := "c1", "c2"
	msgs := []inference.LlmMessage{
		{Role: "user", Content: strings.Repeat("u", 4000)},
		{Role: "assistant", ToolCalls: []inference.ToolCall{{ID: c1, Name: "read"}, {ID: c2, Name: "read"}}},
		{Role: "tool", Content: strings.Repeat("t", 400), ToolCallID: &c1},
		{Role: "tool", Content: strings.Repeat("t", 40), ToolCallID: &c2},
		{Role: "assistant", Content: strings.Repeat("a", 40)},
	}
	// Budget covers the last tool result + reply, landing mid-block.
	keep := estimateMessageTokens(msgs[3]) + estimateMessageTokens(msgs[4])
	if got := splitForCompaction(msgs, keep); got != 4 {
		t.Fatalf("boundary %d, want 4 (skip past orphaned tool result)", got)
	}
	// Everything fits: nothing to compact.
	if got := splitForCompaction(msgs, 1<<40); got != 0 {
		t.Fatalf("boundary %d, want 0", got)
	}
	// A single huge message: nothing older to summarize.
	if got := splitForCompaction(msgs[:1], 1); got != 0 {
		t.Fatalf("boundary %d, want 0", got)
	}
	// Last block alone is over budget: keep that whole block.
	big := []inference.LlmMessage{
		{Role: "user", Content: "task"},
		{Role: "assistant", ToolCalls: []inference.ToolCall{{ID: c1, Name: "read"}}},
		{Role: "tool", Content: strings.Repeat("t", 40000), ToolCallID: &c1},
	}
	if got := splitForCompaction(big, 10); got != 1 {
		t.Fatalf("boundary %d, want 1 (keep the open tool block)", got)
	}
}

func TestReactiveCompactionRecoversOverflow(t *testing.T) {
	prov := &mockProvider{
		streams: []streamScript{
			{err: overflowErr()},
			{chunks: []inference.Chunk{inference.TextChunk("ok")}},
		},
		complete: inference.ToolResponse{Content: "SUM"},
	}
	a, sess, tx, done := testAgent(t, prov)
	defer close(done)
	seedHistory(t, a, sess, 3, 200)
	outcome, err := a.Run()
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Reply != "ok" {
		t.Fatalf("outcome %+v", outcome)
	}
	if len(prov.completeMsgs) != 1 || len(prov.completeMsgs[0]) != 1 {
		t.Fatalf("summary call %+v", prov.completeMsgs)
	}
	if !strings.Contains(prov.completeMsgs[0][0].Content, "old-0") || prov.completeSys[0] == "sys" {
		t.Fatal("summary request must carry the old conversation under the compaction prompt")
	}
	if len(prov.streamMsgs) != 2 {
		t.Fatalf("streams %d", len(prov.streamMsgs))
	}
	after := prov.streamMsgs[1]
	if after[0].Role != "user" || !strings.Contains(after[0].Content, "SUM") || len(after) >= len(prov.streamMsgs[0]) {
		t.Fatalf("compacted view %+v", after)
	}
	if last := after[len(after)-1]; last.Role != "user" || last.Content != "hi" {
		t.Fatalf("current request must survive compaction, got %+v", last)
	}
	st := compactionChunks(drainChunks(tx))
	if len(st) != 2 || st[0].Phase != "start" || st[1].Phase != "done" || st[1].Reason != "overflow" {
		t.Fatalf("status %+v", st)
	}
	// Reloading from disk yields the same model view as the retried round.
	h, err := sess.History()
	if err != nil {
		t.Fatal(err)
	}
	view := LlmMessages(h, true)
	if len(view) != len(after)+1 || view[0].Content != after[0].Content || view[len(view)-1].Content != "ok" {
		t.Fatalf("persisted view %+v vs %+v", view, after)
	}
	if a.retries != 0 {
		t.Fatal("compaction must not spend the retry budget")
	}
}

func TestProactiveCompactionAtThreshold(t *testing.T) {
	prov := &mockProvider{
		streams:  []streamScript{{chunks: []inference.Chunk{inference.TextChunk("ok")}}},
		complete: inference.ToolResponse{Content: "SUM"},
	}
	a, sess, tx, done := testAgent(t, prov)
	defer close(done)
	a.window = 400
	seedHistory(t, a, sess, 3, 400)
	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	if len(prov.completeMsgs) != 1 {
		t.Fatalf("summary calls %d", len(prov.completeMsgs))
	}
	if !strings.Contains(prov.streamMsgs[0][0].Content, "SUM") {
		t.Fatalf("first round not compacted: %+v", prov.streamMsgs[0][0])
	}
	st := compactionChunks(drainChunks(tx))
	if len(st) != 2 || st[1].Reason != "auto" || st[1].TokensAfter >= st[1].TokensBefore {
		t.Fatalf("status %+v", st)
	}
}

func TestProactiveCompactionSkippedBelowThresholdOrUnknownWindow(t *testing.T) {
	for _, window := range []int64{0, 1 << 30} {
		prov := &mockProvider{streams: []streamScript{{chunks: []inference.Chunk{inference.TextChunk("ok")}}}}
		a, sess, _, done := testAgent(t, prov)
		a.window = window
		seedHistory(t, a, sess, 3, 400)
		if _, err := a.Run(); err != nil {
			t.Fatal(err)
		}
		close(done)
		if len(prov.completeMsgs) != 0 {
			t.Fatalf("window %d: unexpected compaction", window)
		}
	}
}

func TestProactiveCompactionFailureContinues(t *testing.T) {
	prov := &mockProvider{
		streams:     []streamScript{{chunks: []inference.Chunk{inference.TextChunk("ok")}}},
		completeErr: errors.New("summary boom"),
	}
	a, sess, tx, done := testAgent(t, prov)
	defer close(done)
	a.window = 400
	seedHistory(t, a, sess, 3, 400)
	outcome, err := a.Run()
	if err != nil || outcome.Reply != "ok" {
		t.Fatalf("outcome %+v err %v", outcome, err)
	}
	st := compactionChunks(drainChunks(tx))
	if len(st) != 2 || st[1].Phase != "failed" || !strings.Contains(st[1].Error, "summary boom") {
		t.Fatalf("status %+v", st)
	}
	h, _ := sess.History()
	for _, m := range h {
		if m.IsCompaction() {
			t.Fatal("failed compaction must not persist a marker")
		}
	}
}

func TestReactiveCompactionFailureReportsOverflow(t *testing.T) {
	prov := &mockProvider{
		streams:     []streamScript{{err: overflowErr()}},
		completeErr: errors.New("summary boom"),
	}
	a, sess, _, done := testAgent(t, prov)
	defer close(done)
	seedHistory(t, a, sess, 3, 200)
	_, err := a.Run()
	if err == nil || !strings.Contains(err.Error(), "prompt is too long") {
		t.Fatalf("err %v", err)
	}
}

func TestOverflowGivesUpAfterUnproductiveCompactions(t *testing.T) {
	prov := &mockProvider{streamsErr: overflowErr(), complete: inference.ToolResponse{Content: "SUM"}}
	a, sess, _, done := testAgent(t, prov)
	defer close(done)
	seedHistory(t, a, sess, 6, 200)
	_, err := a.Run()
	if err == nil || !strings.Contains(err.Error(), "prompt is too long") {
		t.Fatalf("err %v", err)
	}
	if n := len(prov.completeMsgs); n == 0 || n > maxUnproductiveCompactions {
		t.Fatalf("compactions %d", n)
	}
}

// bigReadRounds scripts n tool rounds, each reading its own large file,
// then a final text reply.
func bigReadRounds(t *testing.T, cwd string, n int) []streamScript {
	t.Helper()
	var streams []streamScript
	for i := 0; i < n; i++ {
		name := filepath.Join(cwd, fmt.Sprintf("big%d.txt", i))
		if err := writeTestFile(name, strings.Repeat("y", 2000)); err != nil {
			t.Fatal(err)
		}
		streams = append(streams, callRound(fmt.Sprintf("c%d", i), "read", fmt.Sprintf(`{"path":%q}`, name)))
	}
	return append(streams, streamScript{chunks: []inference.Chunk{inference.TextChunk("ok")}})
}

func TestLongTurnCompactsAsOftenAsNeeded(t *testing.T) {
	sess := testSession(t, "00000000-0000-4000-8000-000000000071")
	prov := &mockProvider{streams: bigReadRounds(t, sess.Meta.Cwd, 6), complete: inference.ToolResponse{Content: "SUM"}}
	a, tx, done := newAgentForTest(t, prov, sess, "go", false, true)
	defer close(done)
	a.window = 1000
	outcome, err := a.Run()
	if err != nil || outcome.Reply != "ok" {
		t.Fatalf("outcome %+v err %v", outcome, err)
	}
	if n := len(prov.completeMsgs); n < 3 {
		t.Fatalf("an uncapped turn must keep compacting, got %d summaries", n)
	}
	for _, st := range compactionChunks(drainChunks(tx)) {
		if st.Phase == "failed" {
			t.Fatalf("unexpected failure %+v", st)
		}
	}
}

func TestProactiveCompactionStopsAfterRepeatedFailures(t *testing.T) {
	sess := testSession(t, "00000000-0000-4000-8000-000000000072")
	prov := &mockProvider{streams: bigReadRounds(t, sess.Meta.Cwd, 6), completeErr: errors.New("summary boom")}
	a, _, done := newAgentForTest(t, prov, sess, "go", false, true)
	defer close(done)
	a.window = 1000
	outcome, err := a.Run()
	if err != nil || outcome.Reply != "ok" {
		t.Fatalf("outcome %+v err %v", outcome, err)
	}
	if n := len(prov.completeMsgs); n != maxUnproductiveCompactions {
		t.Fatalf("summary attempts %d, want %d", n, maxUnproductiveCompactions)
	}
}

func TestOverflowWithNothingToCompactFails(t *testing.T) {
	prov := &mockProvider{streamsErr: overflowErr(), complete: inference.ToolResponse{Content: "SUM"}}
	a, _, _, done := testAgent(t, prov)
	defer close(done)
	_, err := a.Run()
	if err == nil || !strings.Contains(err.Error(), "prompt is too long") {
		t.Fatalf("err %v", err)
	}
	if len(prov.completeMsgs) != 0 {
		t.Fatal("nothing older than the request: no summary call")
	}
}

func TestReactiveCompactionWithLargeSystemPrompt(t *testing.T) {
	prov := &mockProvider{
		streams: []streamScript{
			{err: overflowErr()},
			{chunks: []inference.Chunk{inference.TextChunk("ok")}},
		},
		complete: inference.ToolResponse{Content: "SUM"},
	}
	a, sess, _, done := testAgent(t, prov)
	defer close(done)
	a.system = strings.Repeat("s", 40000)
	seedHistory(t, a, sess, 1, 10)
	if out, err := a.Run(); err != nil || out.Reply != "ok" {
		t.Fatalf("out %+v err %v", out, err)
	}
	if len(prov.completeMsgs) != 1 {
		t.Fatal("the fixed system prompt must not swallow the keep budget")
	}
}
