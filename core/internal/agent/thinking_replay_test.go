package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/inference"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

func copilotState(opaque string) *inference.Thinking {
	return &inference.Thinking{Provider: "github-copilot", Model: "claude-sonnet-5.5", Format: "copilot-chat", Opaque: opaque}
}

func assistantThinking(msgs []inference.LlmMessage) []*inference.Thinking {
	var out []*inference.Thinking
	for _, m := range msgs {
		if m.Role == "assistant" {
			out = append(out, m.Thinking)
		}
	}
	return out
}

func TestReasoningIsKeptAndSentBackOnTheNextRound(t *testing.T) {
	call := inference.ToolCall{ID: "c1", Name: "read", Arguments: `{"path":"nope.txt"}`}
	prov := &mockProvider{streams: []streamScript{
		{chunks: []inference.Chunk{{Reasoning: "Need the "}, {Reasoning: "file."}, {Thinking: copilotState("OPAQUE-1")}, {ToolCalls: []inference.ToolCall{call}}}},
		{chunks: []inference.Chunk{{Reasoning: "It is missing."}, {Thinking: copilotState("OPAQUE-2")}, inference.TextChunk("No such file.")}},
	}}
	a, sess, _, done := testAgent(t, prov)
	defer close(done)
	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}

	// Round two was sent round one's reasoning: its text and its opaque state.
	sent := assistantThinking(prov.streamMsgs[1])
	if len(sent) != 1 || sent[0] == nil || sent[0].Text != "Need the file." || sent[0].Opaque != "OPAQUE-1" ||
		sent[0].Model != "claude-sonnet-5.5" || sent[0].Format != "copilot-chat" {
		t.Fatalf("round 2 got %+v", sent)
	}

	// Both rounds are stored, state included, text-only rounds too.
	msgs, err := sess.History()
	if err != nil {
		t.Fatal(err)
	}
	var stored []string
	for _, m := range msgs {
		if m.Role == transcript.RoleAssistant && m.Thinking != nil {
			stored = append(stored, m.Thinking.Text+"/"+m.Thinking.Opaque)
		}
	}
	if strings.Join(stored, "|") != "Need the file./OPAQUE-1|It is missing./OPAQUE-2" {
		t.Fatalf("stored %q", stored)
	}
}

func TestReasoningSurvivesIntoALaterTurn(t *testing.T) {
	prov := &mockProvider{streams: []streamScript{
		{chunks: []inference.Chunk{{Reasoning: "plan"}, {Thinking: copilotState("OP")}, inference.TextChunk("answer")}},
	}}
	a, sess, _, done := testAgent(t, prov)
	defer close(done)
	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	history, err := sess.History()
	if err != nil {
		t.Fatal(err)
	}
	next := LlmMessages(history, true)
	got := assistantThinking(next)
	if len(got) != 1 || got[0] == nil || got[0].Opaque != "OP" || got[0].Text != "plan" {
		t.Fatalf("next turn messages %+v", got)
	}
}

func TestBufferedFallbackKeepsReasoning(t *testing.T) {
	prov := &mockProvider{
		streams: []streamScript{{}},
		complete: inference.ToolResponse{
			Content: "answer", Reasoning: "buffered plan", Thinking: copilotState("BUF"),
		},
	}
	a, sess, _, done := testAgent(t, prov)
	defer close(done)
	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	history, _ := sess.History()
	var found *transcript.Thinking
	for _, m := range history {
		if m.Role == transcript.RoleAssistant {
			found = m.Thinking
		}
	}
	if found == nil || found.Opaque != "BUF" || found.Text != "buffered plan" {
		t.Fatalf("stored %+v", found)
	}
}

func TestReasoningDoesNotLeakIntoARetriedRound(t *testing.T) {
	fastRetries(t)
	prov := &mockProvider{streams: []streamScript{
		{chunks: []inference.Chunk{{Reasoning: "doomed"}, {Thinking: copilotState("STALE")}}, err: inference.RequestError("p: stream: status 503")},
		{chunks: []inference.Chunk{{Reasoning: "fresh"}, inference.TextChunk("answer")}},
	}}
	a, sess, _, done := testAgent(t, prov)
	defer close(done)
	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	history, _ := sess.History()
	for _, m := range history {
		if m.Role == transcript.RoleAssistant && m.Content == "answer" {
			if m.Thinking == nil || m.Thinking.Opaque != "" || m.Thinking.Text != "fresh" {
				t.Fatalf("answer carries %+v", m.Thinking)
			}
		}
	}
}

func TestSummaryKeepsReasoning(t *testing.T) {
	msgs := []inference.LlmMessage{
		{Role: "user", Content: "do it"},
		{Role: "assistant", Content: "", Thinking: &inference.Thinking{Text: "chose option B because A breaks the build"}},
	}
	out := renderForSummary(msgs, 0)
	if !strings.Contains(out, "chose option B because A breaks the build") {
		t.Fatalf("summary dropped the reasoning:\n%s", out)
	}
}

func TestTranscriptThinkingRoundTrips(t *testing.T) {
	in := transcript.Message{Role: transcript.RoleAssistant, Content: "x", Thinking: &transcript.Thinking{
		Text: "t", Provider: "p", Model: "m", Format: "responses", Opaque: "o", Raw: json.RawMessage(`[{"a":1}]`),
	}}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out transcript.Message
	var got *transcript.Thinking
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	got = out.Thinking
	if got == nil || got.Text != "t" || got.Provider != "p" || got.Model != "m" || got.Format != "responses" ||
		got.Opaque != "o" || string(got.Raw) != `[{"a":1}]` {
		t.Fatalf("round trip %s -> %+v", raw, got)
	}
}

func TestPlainTurnKeepsReasoning(t *testing.T) {
	prov := &mockProvider{streams: []streamScript{
		{chunks: []inference.Chunk{{Reasoning: "weighing it"}, {Thinking: copilotState("PLAIN")}, inference.TextChunk("answer")}},
	}}
	sess := testSession(t, "00000000-0000-4000-8000-000000000041")
	a, _, done := newAgentForTest(t, prov, sess, "hi", false, false)
	defer close(done)
	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	history, err := sess.History()
	if err != nil {
		t.Fatal(err)
	}
	var found *transcript.Message
	for i, m := range history {
		if m.Role == transcript.RoleAssistant {
			found = &history[i]
		}
	}
	if found == nil || found.Reasoning != "weighing it" || found.Thinking == nil || found.Thinking.Opaque != "PLAIN" {
		t.Fatalf("stored %+v", found)
	}
}

func TestPlainBufferedFallbackKeepsReasoning(t *testing.T) {
	prov := &mockProvider{
		streams:  []streamScript{{}},
		complete: inference.ToolResponse{Content: "answer", Reasoning: "buffered", Thinking: copilotState("PB")},
	}
	sess := testSession(t, "00000000-0000-4000-8000-000000000042")
	a, _, done := newAgentForTest(t, prov, sess, "hi", false, false)
	defer close(done)
	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	history, _ := sess.History()
	for _, m := range history {
		if m.Role == transcript.RoleAssistant && (m.Thinking == nil || m.Thinking.Opaque != "PB" || m.Reasoning != "buffered") {
			t.Fatalf("stored %+v", m)
		}
	}
}
