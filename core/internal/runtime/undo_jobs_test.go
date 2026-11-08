package runtime

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Ady-JK/paladin/core/internal/agent"
	"github.com/Ady-JK/paladin/core/internal/tools"
)

func TestFinishedBackgroundJobWakesIdleSession(t *testing.T) {
	rt, ws, sid := undoRuntime(t, `{"cmd":"while [ ! -f wake-gate ]; do sleep 0.05; done; echo job-done","background":true}`, "")
	gate := filepath.Join(ws, "wake-gate")
	t.Cleanup(func() { _ = os.WriteFile(gate, nil, 0o600); rt.ShutdownTurns(5 * time.Second) })
	_, done, err := rt.SubmitTurn(agent.Request{SessionID: sid, Cwd: ws, Message: "start job", Permissions: "full-access"}, SubmitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		if result.Error != "" {
			t.Fatal(result.Error)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("initial turn did not finish")
	}
	if rt.hub.Busy(sid) {
		t.Fatal("session should be idle before the job completes")
	}
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	waitJob(t, rt, sid)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		sess, err := rt.transcripts.Load(ws, sid)
		if err != nil {
			t.Fatal(err)
		}
		msgs, err := sess.History()
		if err != nil {
			t.Fatal(err)
		}
		for _, msg := range msgs {
			if msg.IsNotify && msg.Name != nil && *msg.Name == "shell" {
				return
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("finished job did not wake the session with a completion notice")
}

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
	rt, ws, sid := undoRuntime(t, `{"cmd":"sleep 30","background":true,"notify":false}`, "")
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
	rt, ws, sid := undoRuntime(t, `{"cmd":"echo job > j.txt","background":true,"notify":false}`, "")
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
