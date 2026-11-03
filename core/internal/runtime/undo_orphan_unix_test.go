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
	orphanJob(t, rt, sid, ws, deadPID(t))
	if _, err := rt.RevertSession(sid, "", 0); err != nil {
		t.Fatalf("revert with a dead orphan: %v", err)
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
