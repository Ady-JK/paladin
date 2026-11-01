//go:build linux

package procstat

import (
	"os"
	"syscall"
	"testing"
)

func TestProcListerReadsThisProcess(t *testing.T) {
	procs, err := defaultLister(false)
	if err != nil {
		t.Fatalf("listProc: %v", err)
	}
	if len(procs) != 1 {
		t.Fatalf("all=false returned %d rows, want only self", len(procs))
	}
	got := procs[0]
	if got.PID != os.Getpid() {
		t.Fatalf("pid = %d, want %d", got.PID, os.Getpid())
	}
	if got.PGID != syscall.Getpgrp() {
		t.Errorf("pgid = %d, want %d", got.PGID, syscall.Getpgrp())
	}
	if got.RSS <= 0 {
		t.Errorf("rss = %d, want > 0", got.RSS)
	}
	if got.Start == 0 {
		t.Error("start time missing: PID reuse cannot be detected")
	}
	if got.CPUTime < 0 {
		t.Errorf("cpu time = %v", got.CPUTime)
	}
}

func TestProcListerReadsEveryProcess(t *testing.T) {
	procs, err := defaultLister(true)
	if err != nil {
		t.Fatalf("listProc: %v", err)
	}
	if len(procs) < 2 {
		t.Fatalf("all=true returned %d rows, want the whole table", len(procs))
	}
	seenSelf := false
	for _, p := range procs {
		if p.PID == os.Getpid() {
			seenSelf = true
		}
	}
	if !seenSelf {
		t.Fatal("the core itself is missing from the table")
	}
}
