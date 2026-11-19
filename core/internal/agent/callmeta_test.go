package agent

import (
	"testing"

	"github.com/Ady-JK/paladin/core/internal/inference"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

func TestOnlyTheFirstRoundOfAPromptIsUserInitiated(t *testing.T) {
	call := inference.ToolCall{ID: "c1", Name: "read", Arguments: `{"path":"nope.txt"}`}
	prov := &mockProvider{streams: []streamScript{
		{chunks: []inference.Chunk{{ToolCalls: []inference.ToolCall{call}}}},
		{chunks: []inference.Chunk{inference.TextChunk("done")}},
	}}
	a, _, _, done := testAgent(t, prov)
	defer close(done)
	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	if len(prov.metas) != 2 {
		t.Fatalf("calls %d", len(prov.metas))
	}
	first, second := prov.metas[0], prov.metas[1]
	if first.UserInitiated == nil || !*first.UserInitiated || second.UserInitiated == nil || *second.UserInitiated {
		t.Fatalf("initiators: first %v second %v", first.UserInitiated, second.UserInitiated)
	}
	if first.Kind != inference.CallAgent || first.InteractionID == "" || first.InteractionID != second.InteractionID {
		t.Fatalf("meta %+v / %+v", first, second)
	}
}

func TestARetriedRoundIsNotUserInitiated(t *testing.T) {
	fastRetries(t)
	prov := &mockProvider{streams: []streamScript{
		{err: inference.RequestError("p: stream: status 503")},
		{chunks: []inference.Chunk{inference.TextChunk("ok")}},
	}}
	a, _, _, done := testAgent(t, prov)
	defer close(done)
	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	if len(prov.metas) != 2 || *prov.metas[0].UserInitiated != true || *prov.metas[1].UserInitiated != false {
		t.Fatalf("metas %+v", prov.metas)
	}
}

func TestEachPromptGetsItsOwnInteraction(t *testing.T) {
	ids := map[string]bool{}
	for i := 0; i < 2; i++ {
		prov := &mockProvider{streams: []streamScript{{chunks: []inference.Chunk{inference.TextChunk("ok")}}}}
		a, _, _, done := testAgent(t, prov)
		if _, err := a.Run(); err != nil {
			t.Fatal(err)
		}
		close(done)
		ids[prov.metas[0].InteractionID] = true
	}
	if len(ids) != 2 {
		t.Fatalf("two prompts shared an interaction id: %v", ids)
	}
}

func TestResumeTurnIsNotUserInitiated(t *testing.T) {
	prov := &mockProvider{streams: []streamScript{{chunks: []inference.Chunk{inference.TextChunk("ok")}}}}
	sess := testSession(t, "00000000-0000-4000-8000-000000000051")
	a, _, done := newAgentForTest(t, prov, sess, "", true, true)
	defer close(done)
	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	if len(prov.metas) == 0 || *prov.metas[0].UserInitiated {
		t.Fatalf("metas %+v", prov.metas)
	}
}

func TestSubagentCallsAreMarkedAsSubagent(t *testing.T) {
	prov := &mockProvider{streams: []streamScript{{chunks: []inference.Chunk{inference.TextChunk("ok")}}}}
	sess := testSession(t, "00000000-0000-4000-8000-000000000052")
	sess.Meta.Subagent = &transcript.Subagent{}
	a, _, done := newAgentForTest(t, prov, sess, "hi", false, true)
	defer close(done)
	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	m := prov.metas[0]
	if m.Kind != inference.CallSubagent || *m.UserInitiated {
		t.Fatalf("meta %+v", m)
	}
}
