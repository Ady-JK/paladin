package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/inference"
	"github.com/Ady-JK/paladin/core/internal/permission"
	"github.com/Ady-JK/paladin/core/internal/sandbox"
	"github.com/Ady-JK/paladin/core/internal/tools"
)

type fakeGate struct {
	mu        sync.Mutex
	auth      permission.Auth
	escalate  bool
	seenArgs  []string
	escalated []string
}

func (g *fakeGate) Authorize(_ context.Context, callID, name string, args json.RawMessage, notify func(permission.Approval)) permission.Auth {
	g.mu.Lock()
	g.seenArgs = append(g.seenArgs, string(args))
	g.mu.Unlock()
	notify(permission.Approval{ID: callID, Tool: name, State: permission.StatePending})
	return g.auth
}

func (g *fakeGate) Escalate(_ context.Context, callID, _ string, _ json.RawMessage, notify func(permission.Approval)) bool {
	g.mu.Lock()
	g.escalated = append(g.escalated, callID)
	g.mu.Unlock()
	notify(permission.Approval{ID: callID, Kind: permission.KindEscalate, State: permission.StatePending})
	return g.escalate
}

// sbProbe stands in for shell: it records each call's sandbox and fails
// as sandbox-blocked while confined when blockConfined is set.
type sbProbe struct {
	mu            sync.Mutex
	seen          []*sandbox.Policy
	blockConfined bool
}

func (p *sbProbe) Name() string { return "shell" }
func (p *sbProbe) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{Name: "shell", Parameters: map[string]any{"type": "object"}}
}
func (p *sbProbe) Call(_ json.RawMessage, ctx *tools.ToolCtx) (string, error) {
	p.mu.Lock()
	p.seen = append(p.seen, ctx.Sandbox)
	p.mu.Unlock()
	if p.blockConfined && ctx.Sandbox != nil {
		return "", tools.WithContent(`{"status":"failed","exit_code":1,"output":"Operation not permitted","sandbox_blocked":true}`, "blocked")
	}
	return `{"status":"done","exit_code":0,"output":"ran"}`, nil
}

func gatedRun(t *testing.T, gate ToolGate, probe *sbProbe, args string) []ChatChunk {
	t.Helper()
	prov := &mockProvider{streams: []streamScript{
		{chunks: []inference.Chunk{{ToolCalls: []inference.ToolCall{{ID: "c1", Name: "shell", Arguments: args}}}}},
		{chunks: []inference.Chunk{inference.TextChunk("ok")}},
	}}
	a, _, tx, done := testAgent(t, prov, 5)
	defer close(done)
	a.tools.Register(probe)
	a.gate = gate
	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	return drainChunks(tx)
}

func finalTool(chunks []ChatChunk, id string) *ToolActivity {
	var last *ToolActivity
	for _, c := range chunks {
		if c.Tool != nil && c.Tool.ID == id {
			last = c.Tool
		}
	}
	return last
}

func approvals(chunks []ChatChunk) []permission.Approval {
	var out []permission.Approval
	for _, c := range chunks {
		if c.Approval != nil {
			out = append(out, *c.Approval)
		}
	}
	return out
}

func TestGateDenySkipsTheTool(t *testing.T) {
	probe := &sbProbe{}
	gate := &fakeGate{auth: permission.Auth{Content: `{"error":"denied by the user"}`}}
	chunks := gatedRun(t, gate, probe, `{"cmd":"npm publish"}`)
	if len(probe.seen) != 0 {
		t.Fatal("denied tool ran")
	}
	row := finalTool(chunks, "c1")
	if row == nil || row.Status != "failed" || !strings.Contains(row.Result, "denied") {
		t.Fatalf("row %+v", row)
	}
	if aps := approvals(chunks); len(aps) != 1 || aps[0].ID != "c1" {
		t.Fatalf("approval chunks %+v", aps)
	}
}

func TestGateSandboxReachesTheTool(t *testing.T) {
	probe := &sbProbe{}
	pol := &sandbox.Policy{Mode: sandbox.ModeWorkspaceWrite, Writable: []string{"/ws"}}
	gatedRun(t, &fakeGate{auth: permission.Auth{Run: true, Sandbox: pol}}, probe, `{"cmd":"ls"}`)
	if len(probe.seen) != 1 || probe.seen[0] != pol {
		t.Fatalf("seen %+v", probe.seen)
	}
}

func TestSandboxBlockEscalatesAndReruns(t *testing.T) {
	probe := &sbProbe{blockConfined: true}
	pol := &sandbox.Policy{Mode: sandbox.ModeWorkspaceWrite}
	gate := &fakeGate{auth: permission.Auth{Run: true, Sandbox: pol}, escalate: true}
	chunks := gatedRun(t, gate, probe, `{"cmd":"npm i -g x"}`)
	if len(probe.seen) != 2 || probe.seen[0] == nil || probe.seen[1] != nil {
		t.Fatalf("want confined then unconfined, got %+v", probe.seen)
	}
	if row := finalTool(chunks, "c1"); row == nil || row.Status != "ok" {
		t.Fatalf("row %+v", row)
	}
}

func TestSandboxBlockDeclinedKeepsFailure(t *testing.T) {
	probe := &sbProbe{blockConfined: true}
	gate := &fakeGate{auth: permission.Auth{Run: true, Sandbox: &sandbox.Policy{Mode: sandbox.ModeWorkspaceWrite}}}
	chunks := gatedRun(t, gate, probe, `{"cmd":"npm i -g x"}`)
	if len(probe.seen) != 1 || len(gate.escalated) != 1 {
		t.Fatalf("seen %d escalations %d", len(probe.seen), len(gate.escalated))
	}
	if row := finalTool(chunks, "c1"); row == nil || row.Status != "failed" {
		t.Fatalf("row %+v", row)
	}
}

func TestGateSeesRepairedArguments(t *testing.T) {
	gate := &fakeGate{auth: permission.Auth{Run: true}}
	gatedRun(t, gate, &sbProbe{}, `{"cmd":"rm -rf /",}`)
	if len(gate.seenArgs) != 1 || !json.Valid([]byte(gate.seenArgs[0])) || !strings.Contains(gate.seenArgs[0], "rm -rf") {
		t.Fatalf("gate saw %q", gate.seenArgs)
	}
}
