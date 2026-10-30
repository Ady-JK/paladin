package agent

import (
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/inference"
	"github.com/Ady-JK/paladin/core/internal/session"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

func TestThinkingStreamsLiveAndIsSavedWithItsRound(t *testing.T) {
	call := inference.ToolCall{ID: "c1", Name: "read", Arguments: `{"path":"nope.txt"}`}
	prov := &mockProvider{streams: []streamScript{
		{chunks: []inference.Chunk{{EffortDropped: "max"}, {Reasoning: "Need the "}, {Reasoning: "file."}, {ToolCalls: []inference.ToolCall{call}}}},
		{chunks: []inference.Chunk{{Reasoning: "It is missing."}, inference.TextChunk("No such file.")}},
	}}
	a, sess, tx, done := testAgent(t, prov)
	defer close(done)
	out, err := a.Run()
	if err != nil || out.Reply != "No such file." {
		t.Fatalf("%+v %v", out, err)
	}
	var live strings.Builder
	dropped := ""
	for _, c := range drainChunks(tx) {
		live.WriteString(c.Reasoning)
		if c.ThinkingDropped != "" {
			dropped = c.ThinkingDropped
		}
		if c.Reasoning != "" && c.Delta != "" {
			t.Fatal("thinking must never be answer text")
		}
	}
	if live.String() != "Need the file.It is missing." || dropped != "max" {
		t.Fatalf("live %q dropped %q", live.String(), dropped)
	}
	msgs, err := sess.History()
	if err != nil {
		t.Fatal(err)
	}
	var saved []string
	for _, m := range msgs {
		if m.Role == transcript.RoleAssistant {
			saved = append(saved, m.Reasoning)
		}
	}
	if strings.Join(saved, "|") != "Need the file.|It is missing." {
		t.Fatalf("saved %q", saved)
	}
	for _, m := range prov.streamMsgs[1] {
		if strings.Contains(m.Content, "Need the") {
			t.Fatal("thinking must not be sent back as content")
		}
	}
	var rows []string
	for _, h := range session.HistoryView(msgs) {
		if h.Kind != nil && *h.Kind == "thinking" {
			rows = append(rows, h.Content)
		}
	}
	if strings.Join(rows, "|") != "Need the file.|It is missing." {
		t.Fatalf("history rows %q", rows)
	}
}
