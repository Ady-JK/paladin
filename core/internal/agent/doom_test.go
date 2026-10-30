package agent

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/inference"
)

func textRound(s string) streamScript {
	return streamScript{chunks: []inference.Chunk{inference.TextChunk(s)}}
}

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
	a, _, _, done := testAgent(t, prov)
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
	a, _, _, done := testAgent(t, prov)
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
	a, _, _, done := testAgent(t, prov)
	defer close(done)
	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(toolResultsByID(t, a)["c3"], "repeated") {
		t.Fatal("status polling must not trip the doom-loop guard")
	}
}

func TestDoomGuardRefusesRepeatingCycle(t *testing.T) {
	var g doomGuard
	seq := []struct{ name, args string }{
		{"read", `{"path":"a"}`}, {"grep", `{"pattern":"x"}`},
		{"read", `{"path":"a"}`}, {"grep", `{"pattern":"x"}`},
		{"read", `{"path":"a"}`},
	}
	for i, c := range seq {
		if r := g.check(c.name, c.args); r != "" {
			t.Fatalf("call %d refused too early: %s", i, r)
		}
	}
	r := g.check("grep", `{"pattern":"x"}`)
	if !strings.Contains(r, "cycle") || !strings.Contains(r, "read → grep") {
		t.Fatalf("third lap of an A,B cycle must be refused, got %q", r)
	}
	// Continuing the cycle stays refused.
	if r := g.check("read", `{"path":"a"}`); r == "" {
		t.Fatal("cycle continued without refusal")
	}
}

func TestDoomGuardCyclePeriodThree(t *testing.T) {
	var g doomGuard
	block := []string{"read", "grep", "glob"}
	refused := -1
	for i := 0; i < 9; i++ {
		if r := g.check(block[i%3], `{}`); r != "" && refused < 0 {
			refused = i
		}
	}
	if refused != 8 {
		t.Fatalf("period-3 cycle refused at call %d, want 8", refused)
	}
}

func TestDoomGuardIgnoresProgress(t *testing.T) {
	var g doomGuard
	// Edit/test loops with changing arguments are progress, not a cycle.
	for i := 0; i < 20; i++ {
		if r := g.check("edit", fmt.Sprintf(`{"path":"a","new":"v%d"}`, i)); r != "" {
			t.Fatalf("edit %d refused: %s", i, r)
		}
		if r := g.check("shell", `{"command":"go test ./..."}`); r != "" {
			t.Fatalf("test run %d refused: %s", i, r)
		}
	}
}

func TestDoomGuardPollingResetsCycle(t *testing.T) {
	var g doomGuard
	for i := 0; i < 5; i++ {
		g.check([]string{"read", "grep"}[i%2], `{}`)
	}
	g.check("shell", `{"action":"status","id":"j1"}`)
	if r := g.check("grep", `{}`); r != "" {
		t.Fatalf("polling should reset the cycle window: %s", r)
	}
}
