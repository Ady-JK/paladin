package agent

import (
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/inference"
)

func callRound(id, name, args string) streamScript {
	return streamScript{chunks: []inference.Chunk{{ToolCalls: []inference.ToolCall{{ID: id, Name: name, Arguments: args}}}}}
}

func toolResultsByID(t *testing.T, a *Agent) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, m := range a.messages {
		if m.Role == "tool" && m.ToolCallID != nil {
			out[*m.ToolCallID] = m.Content
		}
	}
	return out
}

func TestDoomLoopBlocksThirdIdenticalCall(t *testing.T) {
	prov := &mockProvider{streams: []streamScript{
		callRound("c1", "read", `{"path":"x.txt"}`),
		callRound("c2", "read", `{ "path" : "x.txt" }`), // same args, different formatting
		callRound("c3", "read", `{"path":"x.txt"}`),
		{chunks: []inference.Chunk{inference.TextChunk("done")}},
	}}
	a, _, _, done := testAgent(t, prov, 10)
	defer close(done)
	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	res := toolResultsByID(t, a)
	if strings.Contains(res["c2"], "repeated") {
		t.Fatalf("second identical call must still run: %s", res["c2"])
	}
	if !strings.Contains(res["c3"], "repeated") {
		t.Fatalf("third identical call must be blocked with guidance, got %s", res["c3"])
	}
}

func TestDoomLoopResetsWhenArgsChange(t *testing.T) {
	prov := &mockProvider{streams: []streamScript{
		callRound("c1", "read", `{"path":"x.txt"}`),
		callRound("c2", "read", `{"path":"x.txt"}`),
		callRound("c3", "read", `{"path":"y.txt"}`),
		callRound("c4", "read", `{"path":"x.txt"}`),
		{chunks: []inference.Chunk{inference.TextChunk("done")}},
	}}
	a, _, _, done := testAgent(t, prov, 10)
	defer close(done)
	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	for id, content := range toolResultsByID(t, a) {
		if strings.Contains(content, "repeated") {
			t.Fatalf("%s blocked although calls were not consecutive: %s", id, content)
		}
	}
}

func TestDoomLoopAllowsJobPolling(t *testing.T) {
	poll := `{"action":"status","job_id":"j1"}`
	prov := &mockProvider{streams: []streamScript{
		callRound("c1", "shell", poll),
		callRound("c2", "shell", poll),
		callRound("c3", "shell", poll),
		{chunks: []inference.Chunk{inference.TextChunk("done")}},
	}}
	a, _, _, done := testAgent(t, prov, 10)
	defer close(done)
	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(toolResultsByID(t, a)["c3"], "repeated") {
		t.Fatal("status polling must not trip the doom-loop guard")
	}
}
