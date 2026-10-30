//go:build unix

package runtime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Ady-JK/paladin/core/internal/tools"
)

// orphanJob writes a job record as an earlier sidecar process would have
// left it: running, never finalized, not known to this runtime.
func orphanJob(t *testing.T, rt *AgentRuntime, sid, ws string, pid int) JobRecord {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), snapTimeout)
	defer cancel()
	before, err := rt.snapRepo(ws).Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rec := &JobRecord{ID: "job-old-1", SessionID: sid, Workspace: ws, CallID: "c-old", Turn: 0,
		StartedAt: time.Now().UTC(), Status: tools.JobRunning, Before: before, PID: pid, dir: sessionDirOf(t, rt, sid)}
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

func TestRevertRefusesWhileAJobFromAnEarlierRunIsAlive(t *testing.T) {
	rt, ws, sid := undoRuntime(t, "echo a > a.txt", "")
	turn(t, rt, sid, ws, "write a")
	cmd := exec.Command("sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); _ = cmd.Wait() }()
	orphanJob(t, rt, sid, ws, cmd.Process.Pid)
	_, err := rt.RevertSession(sid, "", 0)
	if err == nil || !strings.Contains(err.Error(), "earlier run") || !strings.Contains(err.Error(), strconv.Itoa(cmd.Process.Pid)) {
		t.Fatalf("revert with a live orphan: %v", err)
	}
	if readFile(t, filepath.Join(ws, "a.txt")) != "a" {
		t.Fatal("refused revert changed files")
	}
}

func TestRevertReconcilesAnInterruptedJob(t *testing.T) {
	rt, ws, sid := undoRuntime(t, "echo a > a.txt", "")
	turn(t, rt, sid, ws, "write a")
	orphanJob(t, rt, sid, ws, deadPID(t))
	if err := os.WriteFile(filepath.Join(ws, "j.txt"), []byte("job\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pv, err := rt.RevertPreview(sid, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(pv.Conflicts, "j.txt") {
		t.Fatalf("preview should flag the interrupted job's file: %+v", pv)
	}
	res, err := rt.RevertSession(sid, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(res.Restored, "a.txt") || !contains(res.Skipped, "j.txt") {
		t.Fatalf("revert %+v", res)
	}
	if readFile(t, filepath.Join(ws, "j.txt")) != "job" {
		t.Fatal("an interrupted job's file must not be touched")
	}
	recs := readJobRecords(sessionDirOf(t, rt, sid))
	if len(recs) != 1 || !recs[0].Finalized || recs[0].Status != JobInterrupted || strings.Join(recs[0].Uncertain, ",") != "j.txt" || recs[0].Note == "" {
		t.Fatalf("record %+v", recs)
	}
}
