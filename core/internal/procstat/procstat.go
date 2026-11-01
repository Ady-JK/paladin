// Package procstat attributes CPU and memory to sessions by the
// processes their shell commands spawn. The core runs every session as
// goroutines in one process, so the harness itself is reported once
// (Self); what differs per session is its child process trees.
//
// Sampling is one `ps` call, cached for MinGap and shared by concurrent
// callers, and narrowed to the core's own row when nothing is tracked.
package procstat

import (
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MinGap is how long a snapshot is reused before sampling again.
const MinGap = 1500 * time.Millisecond

// Proc is one row of the process table. RSS is in bytes; CPU is a
// percentage of one core (it can exceed 100 on multi-threaded work).
// CPUTime is the cumulative CPU time the process has used; the tracker
// turns it into an instantaneous rate between samples. Start is the
// kernel start time in clock ticks when the lister knows it, so a
// reused PID is not mistaken for the old process. Both may be zero on
// platforms whose lister only reports averages.
type Proc struct {
	PID     int
	PPID    int
	PGID    int
	CPU     float64
	CPUTime time.Duration
	Start   uint64
	RSS     int64
}

// Lister reads the process table. all=false may return only the
// caller's own row.
type Lister func(all bool) ([]Proc, error)

// Usage is a rolled-up CPU/RSS figure.
type Usage struct {
	CPU   float64 `json:"cpu"`
	RSS   int64   `json:"rss"`
	Procs int     `json:"procs"`
}

// Snapshot is one sample: the core itself plus each session's trees.
type Snapshot struct {
	At        int64            `json:"at"`
	Supported bool             `json:"supported"`
	Self      Usage            `json:"self"`
	Sessions  map[string]Usage `json:"sessions"`
}

// Tracker maps root PIDs to the sessions that spawned them.
type Tracker struct {
	list Lister
	self int
	now  func() time.Time

	mu    sync.Mutex
	roots map[int]map[string]int // pid -> session -> refcount
	gen   uint64

	sampleMu sync.Mutex
	last     Snapshot
	lastAt   time.Time
	lastGen  uint64
	have     bool
	prev     map[int]cpuSample
}

// cpuSample is one process's cumulative CPU time at a sample time.
type cpuSample struct {
	at    time.Time
	cpu   time.Duration
	start uint64
}

// New builds a tracker; self is the core's own PID.
func New(list Lister, self int) *Tracker {
	return &Tracker{
		list: list, self: self, now: time.Now,
		roots: map[int]map[string]int{}, prev: map[int]cpuSample{},
	}
}

// Default samples the real process table for this process.
var Default = New(defaultLister, os.Getpid())

// Track attributes pid and its descendants to session until the
// returned func is called. The func is idempotent; bad input is a no-op.
func (t *Tracker) Track(session string, pid int) func() {
	if t == nil || session == "" || pid <= 0 {
		return func() {}
	}
	t.mu.Lock()
	owners := t.roots[pid]
	if owners == nil {
		owners = map[string]int{}
		t.roots[pid] = owners
	}
	owners[session]++
	t.gen++
	t.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			t.mu.Lock()
			defer t.mu.Unlock()
			if owners := t.roots[pid]; owners != nil {
				if owners[session]--; owners[session] <= 0 {
					delete(owners, session)
				}
				if len(owners) == 0 {
					delete(t.roots, pid)
				}
			}
			t.gen++
		})
	}
}

// Snapshot samples (or reuses a fresh sample of) the process table.
func (t *Tracker) Snapshot() Snapshot {
	t.sampleMu.Lock()
	defer t.sampleMu.Unlock()

	t.mu.Lock()
	gen := t.gen
	roots := make(map[int][]string, len(t.roots))
	for pid, owners := range t.roots {
		for s := range owners {
			roots[pid] = append(roots[pid], s)
		}
	}
	t.mu.Unlock()

	now := t.now()
	if t.have && gen == t.lastGen && now.Sub(t.lastAt) < MinGap {
		return clone(t.last)
	}

	snap := Snapshot{At: now.UnixMilli(), Sessions: map[string]Usage{}}
	if t.list != nil {
		if procs, err := t.list(len(roots) > 0); err == nil {
			snap.Supported = true
			t.instantaneous(procs, now)
			attribute(&snap, procs, t.self, roots)
		}
	}
	t.last, t.lastAt, t.lastGen, t.have = snap, now, gen, true
	return clone(snap)
}

func clone(s Snapshot) Snapshot {
	out := s
	out.Sessions = make(map[string]Usage, len(s.Sessions))
	for k, v := range s.Sessions {
		out.Sessions[k] = v
	}
	return out
}

// instantaneous replaces each process's lifetime-average CPU with the
// rate since the previous sample. A process that finishes a burst then
// idles would otherwise keep reporting the burst (ps %cpu is total CPU
// time divided by elapsed lifetime), which reads as a spike that never
// goes away. The first sighting keeps the lister's average; so does a
// reused PID or a sample whose clock went backwards.
func (t *Tracker) instantaneous(procs []Proc, now time.Time) {
	next := make(map[int]cpuSample, len(procs))
	for i := range procs {
		p := &procs[i]
		next[p.PID] = cpuSample{at: now, cpu: p.CPUTime, start: p.Start}
		prev, seen := t.prev[p.PID]
		if !seen || (prev.start != 0 && p.Start != 0 && prev.start != p.Start) {
			continue
		}
		dt := now.Sub(prev.at).Seconds()
		delta := p.CPUTime - prev.cpu
		if dt <= 0 || delta < 0 {
			continue
		}
		p.CPU = float64(delta) / float64(time.Second) / dt * 100
	}
	t.prev = next
}

// attribute walks each root's descendants (by parent and by process
// group) and sums them into its sessions, counting each process once
// per session.
func attribute(snap *Snapshot, procs []Proc, self int, roots map[int][]string) {
	byPID := make(map[int]Proc, len(procs))
	kids := map[int][]int{}
	group := map[int][]int{}
	for _, p := range procs {
		byPID[p.PID] = p
		kids[p.PPID] = append(kids[p.PPID], p.PID)
		group[p.PGID] = append(group[p.PGID], p.PID)
		if p.PID == self {
			snap.Self = Usage{CPU: p.CPU, RSS: p.RSS, Procs: 1}
		}
	}
	seen := map[string]map[int]bool{}
	for root, sessions := range roots {
		if _, alive := byPID[root]; !alive {
			continue
		}
		tree := []int{}
		visited := map[int]bool{}
		stack := []int{root}
		for len(stack) > 0 {
			pid := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if visited[pid] || pid == self {
				continue
			}
			visited[pid] = true
			tree = append(tree, pid)
			stack = append(stack, kids[pid]...)
			if pid == root {
				stack = append(stack, group[root]...)
			}
		}
		for _, s := range sessions {
			if seen[s] == nil {
				seen[s] = map[int]bool{}
			}
			u := snap.Sessions[s]
			for _, pid := range tree {
				if seen[s][pid] {
					continue
				}
				seen[s][pid] = true
				p := byPID[pid]
				u.CPU += p.CPU
				u.RSS += p.RSS
				u.Procs++
			}
			snap.Sessions[s] = u
		}
	}
}

// parsePS reads `pid ppid pgid %cpu rss(KiB) time` rows, skipping junk.
func parsePS(out string) []Proc {
	var procs []Proc
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) != 6 {
			continue
		}
		pid, e1 := strconv.Atoi(f[0])
		ppid, e2 := strconv.Atoi(f[1])
		pgid, e3 := strconv.Atoi(f[2])
		cpu, e4 := strconv.ParseFloat(f[3], 64)
		rss, e5 := strconv.ParseInt(f[4], 10, 64)
		cpuTime, e6 := parseCPUTime(f[5])
		if e1 != nil || e2 != nil || e3 != nil || e4 != nil || e5 != nil || !e6 {
			continue
		}
		procs = append(procs, Proc{PID: pid, PPID: ppid, PGID: pgid, CPU: cpu, CPUTime: cpuTime, RSS: rss * 1024})
	}
	return procs
}

// parseCPUTime reads ps's `[[dd-]hh:]mm:ss[.ff]` CPU-time column into a
// duration; it reports false for anything else so a junk row is skipped.
func parseCPUTime(s string) (time.Duration, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	var days int64
	if i := strings.IndexByte(s, '-'); i >= 0 {
		d, err := strconv.ParseInt(s[:i], 10, 64)
		if err != nil {
			return 0, false
		}
		days, s = d, s[i+1:]
	}
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, false
	}
	sec, err := strconv.ParseFloat(parts[len(parts)-1], 64)
	if err != nil {
		return 0, false
	}
	minutes, err := strconv.ParseInt(parts[len(parts)-2], 10, 64)
	if err != nil {
		return 0, false
	}
	var hours int64
	if len(parts) == 3 {
		if hours, err = strconv.ParseInt(parts[0], 10, 64); err != nil {
			return 0, false
		}
	}
	seconds := float64(days*24*3600+hours*3600+minutes*60) + sec
	return time.Duration(seconds * float64(time.Second)), true
}

// Tracked counts live roots per session.
func (t *Tracker) Tracked() map[string]int {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := map[string]int{}
	for _, owners := range t.roots {
		for s, n := range owners {
			out[s] += n
		}
	}
	return out
}
