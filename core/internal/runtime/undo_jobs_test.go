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

// waitIdle blocks until no turn is running or queued for the session.
// A finished background job wakes the agent with a notify turn that runs
// through the hub after the driving call returned, and RevertSession
// refuses while one is in flight.
func waitIdle(t *testing.T, rt *AgentRuntime, sid string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if !rt.hub.Busy(sid) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("session %s never went idle", sid)
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
	// A finished job wakes the agent with a notify turn through the hub,
	// and that turn outlives the one `turn` driven synchronously above.
	// RevertSession refuses while any turn runs, so wait for the session
	// to go idle rather than racing the notify turn on a slow machine.
	waitIdle(t, rt, sid)
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
