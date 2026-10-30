package agent

import (
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Ady-JK/paladin/core/internal/inference"
	"github.com/Ady-JK/paladin/core/internal/tools"
)

type traceLog struct {
	mu     sync.Mutex
	events []string
}

func (l *traceLog) add(s string) { l.mu.Lock(); l.events = append(l.events, s); l.mu.Unlock() }

// probeTool sleeps, records start/end, and tracks peak concurrency.
type probeTool struct {
	name     string
	parallel bool
	log      *traceLog
	live     *int32
	peak     *int32
}

func (p probeTool) Name() string { return p.name }
func (p probeTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{Name: p.name, Parameters: map[string]any{"type": "object"}}
}
func (p probeTool) ParallelSafe() bool { return p.parallel }
func (p probeTool) Call(args json.RawMessage, ctx *tools.ToolCtx) (string, error) {
	var a struct{ Tag string }
	_ = json.Unmarshal(args, &a)
	n := atomic.AddInt32(p.live, 1)
	for {
		old := atomic.LoadInt32(p.peak)
		if n <= old || atomic.CompareAndSwapInt32(p.peak, old, n) {
			break
		}
	}
	p.log.add("start " + a.Tag)
	time.Sleep(150 * time.Millisecond)
	p.log.add("end " + a.Tag)
	atomic.AddInt32(p.live, -1)
	return fmt.Sprintf(`{"tag":%q}`, a.Tag), nil
}

func TestParallelSafeCallsRunConcurrentlyInOrder(t *testing.T) {
	var live, peak int32
	log := &traceLog{}
	calls := []inference.ToolCall{
		{ID: "p1", Name: "peek", Arguments: `{"tag":"p1"}`},
		{ID: "p2", Name: "peek", Arguments: `{"tag":"p2"}`},
		{ID: "p3", Name: "peek", Arguments: `{"tag":"p3"}`},
		{ID: "x1", Name: "poke", Arguments: `{"tag":"x1"}`},
		{ID: "p4", Name: "peek", Arguments: `{"tag":"p4"}`},
	}
	prov := &mockProvider{streams: []streamScript{
		{chunks: []inference.Chunk{{ToolCalls: calls}}},
		{chunks: []inference.Chunk{inference.TextChunk("done")}},
	}}
	a, _, _, done := testAgent(t, prov)
	defer close(done)
	a.tools.Register(probeTool{name: "peek", parallel: true, log: log, live: &live, peak: &peak})
	a.tools.Register(probeTool{name: "poke", parallel: false, log: log, live: &live, peak: &peak})

	start := time.Now()
	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	if peak < 3 {
		t.Fatalf("p1..p3 should overlap, peak concurrency %d", peak)
	}
	if elapsed > 550*time.Millisecond {
		t.Fatalf("parallel batch took %v; expected ~3 sleep slots not 5", elapsed)
	}
	// The exclusive call is a barrier: it starts after p1..p3 end and
	// finishes before p4 starts.
	idx := map[string]int{}
	for i, e := range log.events {
		idx[e] = i
	}
	for _, p := range []string{"p1", "p2", "p3"} {
		if idx["end "+p] > idx["start x1"] {
			t.Fatalf("x1 started before %s ended: %v", p, log.events)
		}
	}
	if idx["end x1"] > idx["start p4"] {
		t.Fatalf("p4 started before x1 ended: %v", log.events)
	}
	// Results are recorded in call order.
	var order []string
	for _, m := range a.messages {
		if m.Role == "tool" {
			order = append(order, *m.ToolCallID)
		}
	}
	want := []string{"p1", "p2", "p3", "x1", "p4"}
	if fmt.Sprint(order) != fmt.Sprint(want) {
		t.Fatalf("result order %v, want %v", order, want)
	}
}

func TestReadIsParallelSafe(t *testing.T) {
	reg := tools.BuiltinRegistry()
	if !reg.ParallelSafe("read") {
		t.Fatal("read should be parallel-safe")
	}
	for _, n := range []string{"write", "edit", "shell", "ask", "todo", "nope"} {
		if reg.ParallelSafe(n) {
			t.Fatalf("%s must be exclusive", n)
		}
	}
}
