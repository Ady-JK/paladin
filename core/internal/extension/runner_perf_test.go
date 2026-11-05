package extension

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Ady-JK/paladin/core/internal/tools"
)

// The budgets here are the ones the plan commits to; they are gated, not
// aspirational. They measure a trivial plugin on the reference machine (a
// Linux dev box, Node 24 on PATH); a slower machine can move the numbers,
// so each failure prints the measurement.

const (
	// coldStartBudget is spawn + import + activate + first list.
	coldStartBudget = 800 * time.Millisecond
	// warmP50 is the median Go↔runner round trip for an empty call.
	warmP50 = 5 * time.Millisecond
	// warmP99 is the tail of the same measurement.
	warmP99 = 25 * time.Millisecond
	// idleRSS is the resident memory of one warm trivial plugin.
	idleRSS = 80 << 20
	// cachedLoadBudget is Load with ten unchanged plugins: no subprocess.
	cachedLoadBudget = 50 * time.Millisecond
)

// trivialPlugin is a plugin with one empty tool.
var trivialPlugin = map[string]string{"index.ts": `export default { tools: [{
  name: "nil", description: "Do nothing", parameters: { type: "object" },
  execute() { return null; },
}] };`}

func TestColdStartBudget(t *testing.T) {
	if _, err := nodeFor(t); err != nil {
		t.Skip(err)
	}
	dataDir := t.TempDir()
	writePlugin(t, dataDir, "cold", trivialPlugin)
	enable(t, dataDir, "cold")
	reg := newTestRegistry(t, dataDir)
	start := time.Now()
	tools, failures := reg.Load(context.Background())
	elapsed := time.Since(start)
	if len(failures) != 0 || len(tools) != 1 {
		t.Fatalf("%v %v", tools, failures)
	}
	t.Logf("cold start: %v (budget %v)", elapsed, coldStartBudget)
	if elapsed > coldStartBudget {
		t.Fatalf("cold start took %v, over the %v budget", elapsed, coldStartBudget)
	}
}

func TestWarmCallLatency(t *testing.T) {
	if _, err := nodeFor(t); err != nil {
		t.Skip(err)
	}
	dataDir := t.TempDir()
	writePlugin(t, dataDir, "warm", trivialPlugin)
	enable(t, dataDir, "warm")
	reg := newTestRegistry(t, dataDir)
	tools, failures := reg.Load(context.Background())
	if len(failures) != 0 || len(tools) != 1 {
		t.Fatalf("%v %v", tools, failures)
	}
	tool := tools[0]
	// Warm up, so the measurement is the steady state.
	if _, err := callTool(t, tool, map[string]any{}); err != nil {
		t.Fatal(err)
	}
	samples := make([]time.Duration, 0, 100)
	for range 100 {
		start := time.Now()
		if _, err := callTool(t, tool, map[string]any{}); err != nil {
			t.Fatal(err)
		}
		samples = append(samples, time.Since(start))
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	p50, p99 := samples[len(samples)/2], samples[len(samples)*99/100]
	t.Logf("warm call: p50 %v (budget %v), p99 %v (budget %v)", p50, warmP50, p99, warmP99)
	if p50 > warmP50 {
		t.Fatalf("warm p50 %v is over the %v budget", p50, warmP50)
	}
	if p99 > warmP99 {
		t.Fatalf("warm p99 %v is over the %v budget", p99, warmP99)
	}
}

// A warm trivial plugin stays under the memory budget. Linux reads
// /proc; elsewhere the test reports and passes.
func TestWarmRunnerMemory(t *testing.T) {
	if _, err := nodeFor(t); err != nil {
		t.Skip(err)
	}
	dataDir := t.TempDir()
	writePlugin(t, dataDir, "mem", trivialPlugin)
	enable(t, dataDir, "mem")
	reg := newTestRegistry(t, dataDir)
	tools, failures := reg.Load(context.Background())
	if len(failures) != 0 || len(tools) != 1 {
		t.Fatalf("%v %v", tools, failures)
	}
	if _, err := callTool(t, tools[0], map[string]any{}); err != nil {
		t.Fatal(err)
	}
	runners := reg.Runners()
	if len(runners) != 1 {
		t.Fatalf("runners %+v", runners)
	}
	rss, err := residentBytes(runners[0].PID)
	if err != nil {
		if runtime.GOOS == "linux" {
			t.Fatalf("read runner memory: %v", err)
		}
		t.Skipf("cannot read resident memory here: %v", err)
	}
	t.Logf("warm runner RSS: %d MiB (budget %d MiB)", rss>>20, idleRSS>>20)
	if rss > idleRSS {
		t.Fatalf("runner RSS %d exceeds the %d byte budget", rss, idleRSS)
	}
}

// residentBytes reads a process's resident set size.
func residentBytes(pid int) (int64, error) {
	if pid == 0 {
		return 0, os.ErrNotExist
	}
	if runtime.GOOS != "linux" {
		return 0, os.ErrInvalid
	}
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "statm"))
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(data))
	if len(fields) < 2 {
		return 0, os.ErrInvalid
	}
	pages, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return 0, err
	}
	return pages * int64(os.Getpagesize()), nil
}

// Ten unchanged plugins load from cache with no subprocess at all.
func TestCachedLoadBudget(t *testing.T) {
	if _, err := nodeFor(t); err != nil {
		t.Skip(err)
	}
	dataDir := t.TempDir()
	names := make([]string, 0, 10)
	for i := range 10 {
		name := "plugin" + strconv.Itoa(i)
		names = append(names, name)
		writePlugin(t, dataDir, name, map[string]string{"paladin-plugin.json": `{"tools":[{"name":"noop",` +
			`"description":"Do nothing","parameters":{"type":"object"}}]}`,
			"index.ts": "export default { tools: [] };"})
	}
	enable(t, dataDir, names...)
	reg := newTestRegistry(t, dataDir)
	if tools, failures := reg.Load(context.Background()); len(failures) != 0 || len(tools) != 10 {
		t.Fatalf("%v %v", tools, failures)
	}
	spawns := reg.pool.spawns
	start := time.Now()
	for range 5 {
		if tools, failures := reg.Load(context.Background()); len(failures) != 0 || len(tools) != 10 {
			t.Fatalf("%v %v", tools, failures)
		}
	}
	per := time.Since(start) / 5
	t.Logf("cached load: %v per load (budget %v), %d spawns", per, cachedLoadBudget, reg.pool.spawns-spawns)
	if per > cachedLoadBudget {
		t.Fatalf("cached load took %v, over the %v budget", per, cachedLoadBudget)
	}
	if reg.pool.spawns != spawns {
		t.Fatalf("a cached load spawned %d process(es)", reg.pool.spawns-spawns)
	}
}

// Ten plugins cold-start without a spawn storm: the semaphore lets four
// at a time and the rest queue.
func TestColdStartStormIsStaggered(t *testing.T) {
	if _, err := nodeFor(t); err != nil {
		t.Skip(err)
	}
	dataDir := t.TempDir()
	names := make([]string, 0, 6)
	for i := range 6 {
		name := "storm" + strconv.Itoa(i)
		names = append(names, name)
		writePlugin(t, dataDir, name, trivialPlugin)
	}
	enable(t, dataDir, names...)
	reg := newTestRegistry(t, dataDir)
	loaded, failures := reg.Load(context.Background())
	if len(failures) != 0 || len(loaded) != 6 {
		t.Fatalf("%v %v", loaded, failures)
	}
	// Call them all at once, which is what a first turn touching several
	// plugins does: the pool must start them all and answer every call.
	start := time.Now()
	var wg sync.WaitGroup
	errs := make([]error, len(loaded))
	for i, one := range loaded {
		wg.Add(1)
		go func(i int, one tools.Tool) {
			defer wg.Done()
			_, errs[i] = callTool(t, one, map[string]any{})
		}(i, one)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	t.Logf("six concurrent cold starts: %v", time.Since(start))
	if runners := len(reg.Runners()); runners != 6 {
		t.Fatalf("%d runners, want one per plugin", runners)
	}
}

// toolsReload stops the warm runners and the next call works on a fresh
// process.
func TestReloadStopsRunners(t *testing.T) {
	if _, err := nodeFor(t); err != nil {
		t.Skip(err)
	}
	dataDir := t.TempDir()
	writePlugin(t, dataDir, "reload", trivialPlugin)
	enable(t, dataDir, "reload")
	reg := newTestRegistry(t, dataDir)
	tools, failures := reg.Load(context.Background())
	if len(failures) != 0 || len(tools) != 1 {
		t.Fatalf("%v %v", tools, failures)
	}
	if _, err := callTool(t, tools[0], map[string]any{}); err != nil {
		t.Fatal(err)
	}
	pid := reg.Runners()[0].PID
	start := time.Now()
	reg.Reload()
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("reload took %v", elapsed)
	}
	if len(reg.Runners()) != 0 {
		t.Fatalf("reload kept %+v", reg.Runners())
	}
	if processAlive(t, pid) {
		t.Fatalf("the old runner %d survived the reload", pid)
	}
	tools, failures = reg.Load(context.Background())
	if len(failures) != 0 || len(tools) != 1 {
		t.Fatalf("%v %v", tools, failures)
	}
	out, err := callTool(t, tools[0], map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	_ = out
}

// The init template works as written: the scaffolded folder loads and its
// tool answers.
func TestInitExamplePlugin(t *testing.T) {
	if _, err := nodeFor(t); err != nil {
		t.Skip(err)
	}
	dataDir := t.TempDir()
	dir, err := Init(dataDir, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "hello", "paladin-plugin.json")); err != nil {
		t.Fatal(err)
	}
	// Not enabled until named, so nothing runs yet.
	reg := newTestRegistry(t, dataDir)
	if tools, failures := reg.Load(context.Background()); len(tools) != 0 || len(failures) != 0 {
		t.Fatalf("scaffolded plugin ran: %v %v", tools, failures)
	}
	enable(t, dataDir, "hello")
	reg.Reload()
	tools, failures := reg.Load(context.Background())
	if len(failures) != 0 || len(tools) != 1 {
		t.Fatalf("%v %v", tools, failures)
	}
	var value map[string]any
	raw, err := json.Marshal(map[string]any{"name": "Ada"})
	if err != nil {
		t.Fatal(err)
	}
	out, err := tools[0].Call(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(out), &value); err != nil || !strings.Contains(out, "Hello, Ada") {
		t.Fatalf("call %q %v", out, err)
	}
}
