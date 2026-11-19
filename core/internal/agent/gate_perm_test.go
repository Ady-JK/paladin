package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/inference"
	"github.com/Ady-JK/paladin/core/internal/permission"
	"github.com/Ady-JK/paladin/core/internal/tools"
)

type fakeGate struct {
	mu       sync.Mutex
	auth     permission.Auth
	seenArgs []string
}

func (g *fakeGate) Authorize(_ context.Context, callID, name string, args json.RawMessage, notify func(permission.Approval)) permission.Auth {
	g.mu.Lock()
	g.seenArgs = append(g.seenArgs, string(args))
	g.mu.Unlock()
	notify(permission.Approval{ID: callID, Tool: name, State: permission.StatePending})
	return g.auth
}

// sbProbe records whether an approved call reached the shell.
type sbProbe struct {
	mu   sync.Mutex
	seen []bool
}

func (p *sbProbe) Name() string { return "shell" }
func (p *sbProbe) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{Name: "shell", Parameters: map[string]any{"type": "object"}}
}
func (p *sbProbe) Call(_ json.RawMessage, _ *tools.ToolCtx) (string, error) {
	p.mu.Lock()
	p.seen = append(p.seen, true)
	p.mu.Unlock()
	return `{"status":"done","exit_code":0,"output":"ran"}`, nil
}

func gatedRun(t *testing.T, gate ToolGate, probe *sbProbe, args string) []ChatChunk {
	t.Helper()
	prov := &mockProvider{streams: []streamScript{
		{chunks: []inference.Chunk{{ToolCalls: []inference.ToolCall{{ID: "c1", Name: "shell", Arguments: args}}}}},
		{chunks: []inference.Chunk{inference.TextChunk("ok")}},
	}}
	a, _, tx, done := testAgent(t, prov)
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
	chunks := gatedRun(t, gate, probe, `{"reason":"test","cmd":"npm publish"}`)
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

func TestGateSeesRepairedArguments(t *testing.T) {
	gate := &fakeGate{auth: permission.Auth{Run: true}}
	gatedRun(t, gate, &sbProbe{}, `{"reason":"test","cmd":"rm -rf /",}`)
	if len(gate.seenArgs) != 1 || !json.Valid([]byte(gate.seenArgs[0])) || !strings.Contains(gate.seenArgs[0], "rm -rf") {
		t.Fatalf("gate saw %q", gate.seenArgs)
	}
}

// A first write into a folder with unseen AGENTS.md is held before the
// gate: the user is asked once, for the resent call, and the file is
// written only after the agent has the rules.
func TestHeldWriteSkipsTheGateAndRunsWhenResent(t *testing.T) {
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "pkg", "AGENTS.md"), []byte("pkg rules"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(ws, "pkg", "new.go")
	args := `{"reason":"test","path":"` + target + `","content":"package pkg\n"}`
	prov := &mockProvider{streams: []streamScript{
		{chunks: []inference.Chunk{{ToolCalls: []inference.ToolCall{{ID: "c1", Name: "write", Arguments: args}}}}},
		{chunks: []inference.Chunk{{ToolCalls: []inference.ToolCall{{ID: "c2", Name: "write", Arguments: args}}}}},
		{chunks: []inference.Chunk{inference.TextChunk("ok")}},
	}}
	a, _, tx, done := testAgent(t, prov)
	defer close(done)
	a.tools.Register(&tools.WriteTool{})
	a.toolCtx.Workspace, a.toolCtx.SessionDir = ws, t.TempDir()
	gate := &fakeGate{auth: permission.Auth{Run: true}}
	a.gate = gate
	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	chunks := drainChunks(tx)
	first, second := finalTool(chunks, "c1"), finalTool(chunks, "c2")
	if first == nil || first.Status != "failed" || !strings.Contains(first.Result, "pkg rules") {
		t.Fatalf("first write %+v", first)
	}
	if second == nil || second.Status != "ok" {
		t.Fatalf("resent write %+v", second)
	}
	if len(gate.seenArgs) != 1 {
		t.Fatalf("gate asked %d times", len(gate.seenArgs))
	}
	if got, _ := os.ReadFile(target); string(got) != "package pkg\n" {
		t.Fatalf("file %q", got)
	}
}

// trustGate is a fakeGate that also decides project tools.
type trustGate struct {
	fakeGate
	asked []string
	trust bool
}

func (g *trustGate) TrustProject(_ context.Context, callID, tool string, notify func(permission.Approval)) bool {
	g.asked = append(g.asked, callID+" "+tool)
	notify(permission.Approval{ID: callID, Kind: permission.KindProject, Command: tool, State: permission.StatePending})
	return g.trust
}

// trustProbe asks for a project tool from inside its call, as the
// formatter and language servers do.
type trustProbe struct{ answers []bool }

func (p *trustProbe) Name() string { return "shell" }
func (p *trustProbe) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{Name: "shell", Parameters: map[string]any{"type": "object"}}
}
func (p *trustProbe) Call(_ json.RawMessage, ctx *tools.ToolCtx) (string, error) {
	if ctx.TrustProject != nil {
		p.answers = append(p.answers, ctx.TrustProject("node_modules/.bin/prettier"))
	}
	return `{"status":"done","exit_code":0,"output":"ran"}`, nil
}

// A gated call gets the gate's project-tool question, tied to its own
// call id, and the card reaches the UI.
func TestProjectToolsAreAskedThroughTheGate(t *testing.T) {
	prov := &mockProvider{streams: []streamScript{
		{chunks: []inference.Chunk{{ToolCalls: []inference.ToolCall{{ID: "c1", Name: "shell", Arguments: `{"reason":"test","cmd":"ls"}`}}}}},
		{chunks: []inference.Chunk{inference.TextChunk("ok")}},
	}}
	a, _, tx, done := testAgent(t, prov)
	defer close(done)
	probe := &trustProbe{}
	a.tools.Register(probe)
	gate := &trustGate{fakeGate: fakeGate{auth: permission.Auth{Run: true}}}
	a.gate = gate
	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	if len(gate.asked) != 1 || gate.asked[0] != "c1 node_modules/.bin/prettier" || len(probe.answers) != 1 || probe.answers[0] {
		t.Fatalf("asked %v answers %v", gate.asked, probe.answers)
	}
	found := false
	for _, ap := range approvals(drainChunks(tx)) {
		found = found || ap.Kind == permission.KindProject
	}
	if !found {
		t.Fatal("the project-tool card did not reach the UI")
	}
}
