package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Ady-JK/paladin/core/internal/tools"
)

func sessionDirOf(t *testing.T, rt *AgentRuntime, sid string) string {
	t.Helper()
	sess, err := rt.transcripts.Load("", sid)
	if err != nil {
		t.Fatal(err)
	}
	return sess.Dir
}

// waitJob waits for the session's only job record to be finalized.
func waitJob(t *testing.T, rt *AgentRuntime, sid string) JobRecord {
	t.Helper()
	dir := sessionDirOf(t, rt, sid)
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		recs := readJobRecords(dir)
		if len(recs) == 1 && recs[0].Finalized {
			return recs[0]
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("job never finalized: %+v", readJobRecords(dir))
	return JobRecord{}
}

func runningJob(t *testing.T, rt *AgentRuntime, sid string) JobRecord {
	t.Helper()
	recs := readJobRecords(sessionDirOf(t, rt, sid))
	if len(recs) != 1 || recs[0].Status != tools.JobRunning || recs[0].Finalized {
		t.Fatalf("want one running job record, got %+v", recs)
	}
	return recs[0]
}

func TestBackgroundJobWriteIsUndoableOnceFinished(t *testing.T) {
	rt, ws, sid := undoRuntime(t, `{"cmd":"sleep 0.6; echo job > j.txt","background":true}`, "")
	turn(t, rt, sid, ws, "start job")
	rec := runningJob(t, rt, sid)
	if rec.SessionID != sid || rec.Workspace == "" || rec.CallID == "" || rec.StartedAt.IsZero() || rec.Before == "" || rec.PID <= 0 {
		t.Fatalf("record %+v", rec)
	}
	if _, err := rt.RevertSession(sid, "", 0); err == nil || !strings.Contains(err.Error(), "still running") {
		t.Fatalf("revert while running: %v", err)
	}
	rec = waitJob(t, rt, sid)
	if rec.Status != tools.JobDone || strings.Join(rec.Files, ",") != "j.txt" || len(rec.Uncertain) != 0 {
		t.Fatalf("finalized %+v", rec)
	}
	res, err := rt.RevertSession(sid, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.Restored, ",") != "j.txt" || readFile(t, filepath.Join(ws, "j.txt")) != "<missing>" {
		t.Fatalf("revert %+v", res)
	}
}

func TestCancelledJobIsFinalizedOnce(t *testing.T) {
	rt, ws, sid := undoRuntime(t, `{"cmd":"sleep 30; echo late > k.txt","background":true}`, "")
	turn(t, rt, sid, ws, "start job")
	rec := runningJob(t, rt, sid)
	if _, err := rt.shell.Call([]byte(`{"action":"cancel","job_id":"`+rec.ID+`"}`), &tools.ToolCtx{}); err != nil {
		t.Fatal(err)
	}
	rec = waitJob(t, rt, sid)
	if rec.Status != tools.JobCancelled || len(rec.Files) != 0 {
		t.Fatalf("finalized %+v", rec)
	}
	if _, err := os.Stat(filepath.Join(ws, "k.txt")); err == nil {
		t.Fatal("cancelled job wrote its file")
	}
	if _, err := rt.RevertSession(sid, "", 0); err != nil {
		t.Fatalf("revert after cancel: %v", err)
	}
}

func TestTimedOutJobKeepsItsEarlyWritesUndoable(t *testing.T) {
	rt, ws, sid := undoRuntime(t, `{"cmd":"sleep 0.2; echo early > t.txt; sleep 30","background":true,"timeout_ms":800}`, "")
	turn(t, rt, sid, ws, "start job")
	rec := waitJob(t, rt, sid)
	if rec.Status != tools.JobTimeout || strings.Join(rec.Files, ",") != "t.txt" {
		t.Fatalf("finalized %+v", rec)
	}
	res, err := rt.RevertSession(sid, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.Restored, ",") != "t.txt" {
		t.Fatalf("revert %+v", res)
	}
}

// Another session changes a file the job also writes while it runs: that
// file is uncertain and a revert leaves it alone; the job's other file
// is still restored.
func TestJobFileTouchedConcurrentlyIsUncertain(t *testing.T) {
	rt, ws, sidA := undoRuntime(t,
		`{"cmd":"sleep 1; echo job > j.txt; echo job >> shared.txt","background":true}`, "",
		"echo b > shared.txt", "")
	turn(t, rt, sidA, ws, "start job")
	sessB, err := rt.OpenSession("", ws)
	if err != nil {
		t.Fatal(err)
	}
	turn(t, rt, sessB.SessionID, ws, "b edits")
	rec := waitJob(t, rt, sidA)
	if strings.Join(rec.Files, ",") != "j.txt" || strings.Join(rec.Uncertain, ",") != "shared.txt" {
		t.Fatalf("finalized %+v", rec)
	}
	res, err := rt.RevertSession(sidA, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.Restored, ",") != "j.txt" || strings.Contains(strings.Join(res.Restored, ","), "shared.txt") {
		t.Fatalf("revert %+v", res)
	}
	if got := readFile(t, filepath.Join(ws, "shared.txt")); got != "b\njob" {
		t.Fatalf("shared.txt = %q", got)
	}
}
