//go:build unix

package runtime

import (
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/Ady-JK/paladin/core/internal/tools"
)

// orphanJob writes a job record as an earlier sidecar process would have
// left it: running, never finalized, not known to this runtime.
func orphanJob(t *testing.T, rt *AgentRuntime, sid, ws string, pid int) JobRecord {
	t.Helper()
	rec := &JobRecord{ID: "job-old-1", SessionID: sid, Workspace: ws, CallID: "c-old", Turn: 0,
		StartedAt: time.Now().UTC(), Status: tools.JobRunning, PID: pid, dir: sessionDirOf(t, rt, sid)}
	if err := writeJobRecord(rec); err != nil {
		t.Fatal(err)
	}
	return *rec
}

func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.Process.Pid
}

func TestRevertRefusesWhileAnOrphanJobIsAlive(t *testing.T) {
	rt, ws, sid := undoRuntime(t, "")
	turn(t, rt, sid, ws, "hi")
	orphanJob(t, rt, sid, ws, syscall.Getpgrp())
	if _, err := rt.RevertSession(sid, "", 0); err == nil {
		t.Fatal("revert ran while an old job's process group was alive")
	}
	// The preview reports the blocker so the UI can offer to stop it.
	pv, err := rt.RevertPreview(sid, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pv.BlockingJobs) != 1 || pv.BlockingJobs[0].ID != "job-old-1" || pv.BlockingJobs[0].Live {
		t.Fatalf("preview blocking jobs = %+v", pv.BlockingJobs)
	}
	orphanJob(t, rt, sid, ws, deadPID(t))
	if _, err := rt.RevertSession(sid, "", 0); err != nil {
		t.Fatalf("revert with a dead orphan: %v", err)
	}
}

// RevertKillJobs stops an alive orphan's process group, finalizes its
// record, and lets the revert through — the running-jobs panel cannot
// do any of that for a job an earlier run left behind.
func TestRevertKillJobsStopsAnAliveOrphan(t *testing.T) {
	rt, ws, sid := undoRuntime(t, "")
	turn(t, rt, sid, ws, "hi")
	// A real orphaned process group: spawn one and drop Paladin's
	// knowledge of it, leaving only the record.
	cmd := exec.Command("sh", "-c", "sleep 30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
	orphanJob(t, rt, sid, ws, pid)

	if _, err := rt.RevertSession(sid, "", 0); err == nil {
		t.Fatal("revert ran while an orphaned group was alive")
	}
	stopped, err := rt.RevertKillJobs(sid, "")
	if err != nil || len(stopped) != 1 || stopped[0] != "job-old-1" {
		t.Fatalf("kill jobs = %v, %v", stopped, err)
	}
	// The kill left our child a zombie until we reap it — in production
	// the orphan's parent is the dead sidecar, so init reaps at once.
	_, _ = cmd.Process.Wait()
	deadline := time.Now().Add(5 * time.Second)
	for groupAlive(pid) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if groupAlive(pid) {
		t.Fatal("orphan process group survived the kill")
	}
	if _, err := rt.RevertSession(sid, "", 0); err != nil {
		t.Fatalf("revert after stopping the orphan: %v", err)
	}
}

func TestRevertLeavesAnOrphanJobRecordAlone(t *testing.T) {
	rt, ws, sid := undoRuntime(t, "echo a > a.txt", "")
	turn(t, rt, sid, ws, "write")
	orphanJob(t, rt, sid, ws, deadPID(t))
	if _, err := rt.RevertSession(sid, "", 0); err != nil {
		t.Fatal(err)
	}
	if readFile(t, filepath.Join(ws, "a.txt")) != "<missing>" {
		t.Fatal("folder not restored")
	}
}
