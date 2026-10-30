package agent

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/inference"
	"github.com/Ady-JK/paladin/core/internal/permission"
	"github.com/Ady-JK/paladin/core/internal/sandbox"
	"github.com/Ady-JK/paladin/core/internal/tools"
)

type fakeSteps struct {
	mu    sync.Mutex
	calls []string
}

func (f *fakeSteps) Track(_ context.Context, callID, name string, run func() tools.ToolResult) tools.ToolResult {
	f.mu.Lock()
	f.calls = append(f.calls, callID+":"+name)
	f.mu.Unlock()
	return run()
}

type mutProbe struct{ sbProbe }

func (*mutProbe) Mutates() bool { return true }

type readProbe struct{}

func (readProbe) Name() string { return "peek" }
func (readProbe) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{Name: "peek", Parameters: map[string]any{"type": "object"}}
}
func (readProbe) Call(json.RawMessage, *tools.ToolCtx) (string, error) { return `{"ok":true}`, nil }
func (readProbe) ParallelSafe() bool                                   { return true }

func stepRun(t *testing.T, gate ToolGate, rec StepRecorder, probe *mutProbe) {
	t.Helper()
	prov := &mockProvider{streams: []streamScript{
		{chunks: []inference.Chunk{{ToolCalls: []inference.ToolCall{
			{ID: "r1", Name: "peek", Arguments: `{}`},
			{ID: "m1", Name: "shell", Arguments: `{"cmd":"touch x"}`},
		}}}},
		{chunks: []inference.Chunk{inference.TextChunk("ok")}},
	}}
	a, _, tx, done := testAgent(t, prov)
	defer close(done)
	a.tools.Register(probe)
	a.tools.Register(readProbe{})
	a.gate = gate
	a.steps = rec
	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	drainChunks(tx)
}

func TestStepRecorderWrapsOnlyMutatingCalls(t *testing.T) {
	rec := &fakeSteps{}
	probe := &mutProbe{}
	stepRun(t, nil, rec, probe)
	if len(rec.calls) != 1 || rec.calls[0] != "m1:shell" {
		t.Fatalf("tracked %v", rec.calls)
	}
	if len(probe.seen) != 1 {
		t.Fatalf("probe ran %d times", len(probe.seen))
	}
}

func TestStepRecorderSkipsDeniedCallsAndTracksEscalatedRerun(t *testing.T) {
	rec := &fakeSteps{}
	stepRun(t, &fakeGate{auth: permission.Auth{Content: `{"error":"denied"}`}}, rec, &mutProbe{})
	if len(rec.calls) != 0 {
		t.Fatalf("denied call tracked: %v", rec.calls)
	}
	rec = &fakeSteps{}
	pol := &sandbox.Policy{Mode: sandbox.ModeWorkspaceWrite}
	probe := &mutProbe{sbProbe{blockConfined: true}}
	stepRun(t, &fakeGate{auth: permission.Auth{Run: true, Sandbox: pol}, escalate: true}, rec, probe)
	if len(rec.calls) != 2 || len(probe.seen) != 2 {
		t.Fatalf("tracked %v, ran %d", rec.calls, len(probe.seen))
	}
}

func TestRegistryMutates(t *testing.T) {
	r := tools.NewRegistry()
	r.Register(&tools.WriteTool{})
	r.Register(&tools.EditTool{})
	r.Register(&tools.ReadTool{})
	r.Register(&mutProbe{})
	for name, want := range map[string]bool{"write": true, "edit": true, "shell": true, "read": false, "nope": false} {
		if got := r.Mutates(name); got != want {
			t.Errorf("Mutates(%q) = %v", name, got)
		}
	}
}
