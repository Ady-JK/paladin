package procstat

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

func newTracker(procs []Proc, calls *atomic.Int32, wantAll *atomic.Bool) (*Tracker, *fakeClock) {
	clock := &fakeClock{t: time.Unix(1000, 0)}
	tr := New(func(all bool) ([]Proc, error) {
		calls.Add(1)
		if wantAll != nil {
			wantAll.Store(all)
		}
		return procs, nil
	}, 100)
	tr.now = clock.now
	return tr, clock
}

func TestParsePS(t *testing.T) {
	out := "  100     1   100  2.5  20480 00:00:07\n  201   100   201 50.0  1024 1-02:03:04\nbad line\n  202   201   201  0.0     8 00:00:00.50\n"
	got := parsePS(out)
	want := []Proc{
		{PID: 100, PPID: 1, PGID: 100, CPU: 2.5, CPUTime: 7 * time.Second, RSS: 20480 * 1024},
		{PID: 201, PPID: 100, PGID: 201, CPU: 50, CPUTime: (26*3600 + 3*60 + 4) * time.Second, RSS: 1024 * 1024},
		{PID: 202, PPID: 201, PGID: 201, CPU: 0, CPUTime: 500 * time.Millisecond, RSS: 8 * 1024},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d procs, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("proc %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestParseCPUTime(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"00:00:07", 7 * time.Second, true},
		{"1:02", 62 * time.Second, true},
		{"01:02:03", time.Hour + 2*time.Minute + 3*time.Second, true},
		{"2-03:04:05", 51*time.Hour + 4*time.Minute + 5*time.Second, true},
		{"00:00:00.50", 500 * time.Millisecond, true},
		{"", 0, false},
		{"7", 0, false},
		{"a:b", 0, false},
	}
	for _, tc := range cases {
		got, ok := parseCPUTime(tc.in)
		if ok != tc.ok || got != tc.want {
			t.Errorf("parseCPUTime(%q) = %v, %v; want %v, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestSnapshotUsesInstantaneousCPU(t *testing.T) {
	procs := []Proc{
		{PID: 100, PPID: 1, PGID: 100, CPU: 1, CPUTime: 10 * time.Second, Start: 5},
		{PID: 201, PPID: 100, PGID: 201, CPU: 80, CPUTime: 1 * time.Second, Start: 90},
	}
	tr := New(func(bool) ([]Proc, error) { return procs, nil }, 100)
	clock := &fakeClock{t: time.Unix(2000, 0)}
	tr.now = clock.now
	tr.Track("a", 201)

	// The first sighting has no previous sample, so it keeps ps's
	// lifetime average.
	if got := tr.Snapshot().Sessions["a"].CPU; got != 80 {
		t.Fatalf("first sample CPU = %v, want the lifetime average 80", got)
	}
	// 1.5s of CPU over 3s of wall time is 50% of one core, even though
	// the lifetime average would still read 80.
	procs[1].CPUTime += 1500 * time.Millisecond
	clock.t = clock.t.Add(3 * time.Second)
	if got := tr.Snapshot().Sessions["a"].CPU; got != 50 {
		t.Fatalf("second sample CPU = %v, want 50", got)
	}
	// No CPU time used since the last sample: idle, not a stale spike.
	clock.t = clock.t.Add(3 * time.Second)
	if got := tr.Snapshot().Sessions["a"].CPU; got != 0 {
		t.Fatalf("idle sample CPU = %v, want 0", got)
	}
	// A reused PID (new start time) falls back to the lister's average
	// instead of reporting a negative or bogus rate.
	procs[1] = Proc{PID: 201, PPID: 100, PGID: 201, CPU: 7, CPUTime: 100 * time.Millisecond, Start: 91}
	clock.t = clock.t.Add(3 * time.Second)
	if got := tr.Snapshot().Sessions["a"].CPU; got != 7 {
		t.Fatalf("reused pid CPU = %v, want the fallback 7", got)
	}
}

func TestSnapshotAttributesProcessTreesToSessions(t *testing.T) {
	procs := []Proc{
		{PID: 100, PPID: 1, PGID: 100, CPU: 3, RSS: 30 << 20},   // the core itself
		{PID: 201, PPID: 100, PGID: 201, CPU: 10, RSS: 1 << 20}, // session a: sh
		{PID: 202, PPID: 201, PGID: 201, CPU: 40, RSS: 4 << 20}, // a: child
		{PID: 203, PPID: 202, PGID: 203, CPU: 5, RSS: 2 << 20},  // a: grandchild in own group
		{PID: 301, PPID: 100, PGID: 301, CPU: 1, RSS: 1 << 20},  // session b
		{PID: 999, PPID: 1, PGID: 999, CPU: 90, RSS: 9 << 20},   // unrelated
	}
	var calls atomic.Int32
	tr, _ := newTracker(procs, &calls, nil)
	tr.Track("a", 201)
	tr.Track("b", 301)

	snap := tr.Snapshot()
	if !snap.Supported {
		t.Fatal("snapshot should be supported")
	}
	if snap.Self != (Usage{CPU: 3, RSS: 30 << 20, Procs: 1}) {
		t.Errorf("self = %+v", snap.Self)
	}
	if got := snap.Sessions["a"]; got != (Usage{CPU: 55, RSS: 7 << 20, Procs: 3}) {
		t.Errorf("a = %+v", got)
	}
	if got := snap.Sessions["b"]; got != (Usage{CPU: 1, RSS: 1 << 20, Procs: 1}) {
		t.Errorf("b = %+v", got)
	}
	if len(snap.Sessions) != 2 {
		t.Errorf("sessions = %+v", snap.Sessions)
	}
}

func TestSnapshotSumsSeveralRootsPerSessionWithoutDoubleCounting(t *testing.T) {
	procs := []Proc{
		{PID: 100, PPID: 1, PGID: 100},
		{PID: 201, PPID: 100, PGID: 201, CPU: 10, RSS: 1 << 20},
		{PID: 202, PPID: 201, PGID: 201, CPU: 10, RSS: 1 << 20},
	}
	var calls atomic.Int32
	tr, _ := newTracker(procs, &calls, nil)
	tr.Track("a", 201)
	tr.Track("a", 202) // a nested root must not count 202 twice
	if got := tr.Snapshot().Sessions["a"]; got != (Usage{CPU: 20, RSS: 2 << 20, Procs: 2}) {
		t.Errorf("a = %+v", got)
	}
}

func TestUntrackDropsTheSessionAndIgnoresBadInput(t *testing.T) {
	procs := []Proc{{PID: 100, PGID: 100}, {PID: 201, PPID: 100, PGID: 201, CPU: 1}}
	var calls atomic.Int32
	var all atomic.Bool
	tr, clock := newTracker(procs, &calls, &all)
	untrack := tr.Track("a", 201)
	tr.Track("", 5)()  // no session: no-op
	tr.Track("b", 0)() // no pid: no-op
	if _, ok := tr.Snapshot().Sessions["a"]; !ok {
		t.Fatal("a should be tracked")
	}
	if !all.Load() {
		t.Error("tracked roots need the full process list")
	}
	untrack()
	untrack() // idempotent
	clock.t = clock.t.Add(time.Minute)
	snap := tr.Snapshot()
	if len(snap.Sessions) != 0 {
		t.Errorf("sessions after untrack = %+v", snap.Sessions)
	}
	if all.Load() {
		t.Error("with nothing tracked only the core's own row is needed")
	}
}

func TestSnapshotIsCachedWithinTheMinimumGap(t *testing.T) {
	var calls atomic.Int32
	tr, clock := newTracker([]Proc{{PID: 100, PGID: 100}}, &calls, nil)
	tr.Snapshot()
	tr.Snapshot()
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1 (cached)", calls.Load())
	}
	clock.t = clock.t.Add(MinGap)
	tr.Snapshot()
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2 after the gap", calls.Load())
	}
}

func TestTrackingInvalidatesTheCache(t *testing.T) {
	var calls atomic.Int32
	tr, _ := newTracker([]Proc{{PID: 100, PGID: 100}, {PID: 201, PPID: 100, PGID: 201}}, &calls, nil)
	tr.Snapshot()
	tr.Track("a", 201)
	if _, ok := tr.Snapshot().Sessions["a"]; !ok {
		t.Fatal("a newly tracked root must show without waiting out the cache")
	}
}

func TestConcurrentSnapshotsShareOneSample(t *testing.T) {
	var calls atomic.Int32
	gate := make(chan struct{})
	tr := New(func(bool) ([]Proc, error) {
		calls.Add(1)
		<-gate
		return []Proc{{PID: 100}}, nil
	}, 100)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); tr.Snapshot() }()
	}
	time.Sleep(20 * time.Millisecond)
	close(gate)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", calls.Load())
	}
}

func TestListerFailureReportsUnsupportedWithoutPanicking(t *testing.T) {
	tr := New(func(bool) ([]Proc, error) { return nil, errors.New("no ps") }, 100)
	tr.Track("a", 201)
	snap := tr.Snapshot()
	if snap.Supported {
		t.Error("a failed sample is not supported")
	}
	if snap.Sessions == nil {
		t.Error("sessions should be an empty map, not nil, for JSON")
	}
}

func TestNilListerIsUnsupported(t *testing.T) {
	if New(nil, 1).Snapshot().Supported {
		t.Error("nil lister should be unsupported")
	}
}
