package runtime

import (
	"path/filepath"
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
	dir := sessionDirOf(t, rt, sid)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		recs := readJobRecords(dir)
		if len(recs) == 1 && recs[0].Status == tools.JobRunning && !recs[0].Finalized {
			return recs[0]
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("want one running job record, got %+v", readJobRecords(dir))
	return JobRecord{}
}

func TestRevertRefusesWhileAJobIsRunning(t *testing.T) {
	rt, ws, sid := undoRuntime(t, `{"cmd":"sleep 30","background":true}`, "")
	turn(t, rt, sid, ws, "start job")
	rec := runningJob(t, rt, sid)
	if _, err := rt.RevertSession(sid, "", 0); err == nil {
		t.Fatal("revert ran while a job was still writing")
	}
	if _, err := rt.shell.Call([]byte(`{"action":"cancel","job_id":"`+rec.ID+`"}`), &tools.ToolCtx{SessionID: sid}); err != nil {
		t.Fatal(err)
	}
	rec = waitJob(t, rt, sid)
	if rec.Status != tools.JobCancelled {
		t.Fatalf("finalized %+v", rec)
	}
	if _, err := rt.RevertSession(sid, "", 0); err != nil {
		t.Fatalf("revert after the job stopped: %v", err)
	}
}

func TestRevertAfterAFinishedJob(t *testing.T) {
	rt, ws, sid := undoRuntime(t, `{"cmd":"echo job > j.txt","background":true}`, "")
	turn(t, rt, sid, ws, "run it")
	waitJob(t, rt, sid)
	res, err := rt.RevertSession(sid, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Restored) != 0 {
		t.Fatalf("revert %+v", res)
	}
	if readFile(t, filepath.Join(ws, "j.txt")) != "<missing>" {
		t.Fatal("the turn snapshot was not restored")
	}
}
