// wait_job_test.go covers the wait tool against real shell jobs: a named
// job target blocks until that job exits and returns its exit code and
// output; a no-target wait ends at the first completion and batches the
// rest; nothing running returns immediately instead of holding the turn.

package runtime

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Ady-JK/paladin/core/internal/tools"
)

func jobCtx(t *testing.T, rt *AgentRuntime, sid, ws string) *tools.ToolCtx {
	t.Helper()
	ctx := toolCtx(sid, ws)
	ctx.SessionDir = sessionDirOf(t, rt, sid)
	return ctx
}

func waitForTool(t *testing.T, rt *AgentRuntime, sid, ws, args string) (map[string]any, error) {
	t.Helper()
	tool, ok := rt.tools.Get("wait")
	if !ok {
		t.Fatal("wait is not registered")
	}
	out, err := tool.Call(json.RawMessage(args), jobCtx(t, rt, sid, ws))
	var res map[string]any
	if json.Unmarshal([]byte(out), &res) != nil && err == nil {
		t.Fatalf("wait result is not JSON: %q", out)
	}
	return res, err
}

// A wait naming a job id blocks until that job exits and answers with its
// exit code and output tail, marked reported so no notice repeats it.
func TestWaitTargetsAJobAndReturnsItsResult(t *testing.T) {
	setJobWatch(t, 10*time.Minute, 30*time.Minute, time.Hour)
	rt, ws, sid, _ := agentRuntime(t, func(c modelCall) modelReply { return modelReply{Text: "ok"} })

	shell, _ := rt.tools.Get("shell")
	started, err := shell.Call(json.RawMessage(`{"action":"run","command":"echo target-done","background":true,"notify":true}`), jobCtx(t, rt, sid, ws))
	if err != nil {
		t.Fatal(err)
	}
	var spawn struct {
		JobID string `json:"job_id"`
	}
	if json.Unmarshal([]byte(started), &spawn) != nil || spawn.JobID == "" {
		t.Fatalf("shell start = %q", started)
	}

	res, err := waitForTool(t, rt, sid, ws, `{"target":"`+spawn.JobID+`","timeout_ms":30000}`)
	if err != nil {
		t.Fatal(err)
	}
	if res["timed_out"] == true {
		t.Fatalf("wait timed out: %v", res)
	}
	jobs, _ := res["jobs"].([]any)
	if len(jobs) != 1 {
		t.Fatalf("jobs = %v", res)
	}
	row, _ := jobs[0].(map[string]any)
	if row["job_id"] != spawn.JobID || row["status"] != "done" || row["exit_code"] != float64(0) {
		t.Fatalf("job row = %v", row)
	}
	if out, _ := row["output"].(string); !strings.Contains(out, "target-done") {
		t.Fatalf("output = %q", row["output"])
	}
	// Consumed by the wait: the shell owes no further notice for it.
	if rt.shell.HasNotify(sid) {
		t.Fatal("wait consumed the completion but it is still due a notice")
	}
}

// A no-target wait ends at the first job completion; a second job that
// finishes meanwhile rides along in the same batch.
func TestWaitWithNoTargetBatchesEveryFinishedJob(t *testing.T) {
	setJobWatch(t, 10*time.Minute, 30*time.Minute, time.Hour)
	rt, ws, sid, _ := agentRuntime(t, func(c modelCall) modelReply { return modelReply{Text: "ok"} })

	shell, _ := rt.tools.Get("shell")
	for _, cmd := range []string{"echo one", "echo two"} {
		if _, err := shell.Call(json.RawMessage(`{"action":"run","command":"`+cmd+`","background":true,"notify":true}`), jobCtx(t, rt, sid, ws)); err != nil {
			t.Fatal(err)
		}
	}

	res, err := waitForTool(t, rt, sid, ws, `{"timeout_ms":30000}`)
	if err != nil {
		t.Fatal(err)
	}
	if res["timed_out"] == true {
		t.Fatalf("wait timed out: %v", res)
	}
	jobs, _ := res["jobs"].([]any)
	if len(jobs) < 1 {
		t.Fatalf("expected at least one job row, got %v", res)
	}
	if rt.shell.HasNotify(sid) {
		t.Fatal("the batch should have consumed every finished job's notice")
	}
}

// With nothing running, wait returns immediately with a note rather than
// holding the turn open.
func TestWaitWithNothingRunningReturnsAtOnce(t *testing.T) {
	rt, ws, sid, _ := agentRuntime(t, func(c modelCall) modelReply { return modelReply{Text: "ok"} })
	done := make(chan map[string]any, 1)
	go func() {
		res, err := waitForTool(t, rt, sid, ws, `{}`)
		if err != nil {
			t.Error(err)
		}
		done <- res
	}()
	select {
	case res := <-done:
		if note, _ := res["note"].(string); note == "" {
			t.Fatalf("expected a nothing-to-wait-for note, got %v", res)
		}
		agents, _ := res["agents"].([]any)
		if len(agents) != 0 {
			t.Fatalf("agents = %v", res)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("wait with nothing running did not return promptly")
	}
}

// A target that names neither a child nor a running job is an error the
// model can fix, not a silent wait until timeout.
func TestWaitRejectsAnUnknownTarget(t *testing.T) {
	rt, ws, sid, _ := agentRuntime(t, func(c modelCall) modelReply { return modelReply{Text: "ok"} })
	_, err := waitForTool(t, rt, sid, ws, `{"target":"job-nope"}`)
	if err == nil || !strings.Contains(err.Error(), "unknown target") {
		t.Fatalf("unknown target err = %v", err)
	}
}
