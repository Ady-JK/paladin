package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type recordingObserver struct {
	mu       sync.Mutex
	started  []JobEvent
	finished []JobEvent
}

func (o *recordingObserver) JobStarted(e JobEvent) {
	o.mu.Lock()
	o.started = append(o.started, e)
	o.mu.Unlock()
}

func (o *recordingObserver) JobSpawned(JobEvent) {}

func (o *recordingObserver) JobFinished(e JobEvent) {
	o.mu.Lock()
	o.finished = append(o.finished, e)
	o.mu.Unlock()
}

func (o *recordingObserver) finishedFor(id string) []JobEvent {
	o.mu.Lock()
	defer o.mu.Unlock()
	var out []JobEvent
	for _, e := range o.finished {
		if e.ID == id {
			out = append(out, e)
		}
	}
	return out
}

func startJob(t *testing.T, tool *ShellTool, ctx *ToolCtx, args string) string {
	t.Helper()
	out, err := tool.Call(json.RawMessage(args), ctx)
	if err != nil {
		t.Fatal(err)
	}
	var resp ShellResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil || resp.JobID == nil {
		t.Fatalf("no job id in %s", out)
	}
	return *resp.JobID
}

func waitFinished(t *testing.T, o *recordingObserver, id string) JobEvent {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if got := o.finishedFor(id); len(got) > 0 {
			// Give a duplicate a chance to show up.
			time.Sleep(200 * time.Millisecond)
			if got = o.finishedFor(id); len(got) != 1 {
				t.Fatalf("job %s finished %d times", id, len(got))
			}
			return got[0]
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("job %s never finished", id)
	return JobEvent{}
}

// Every background job reports its start, then its end exactly once,
// whether it exits, is cancelled or times out.
func TestJobObserverFinalizesOnce(t *testing.T) {
	dir := t.TempDir()
	ws := filepath.Join(dir, "ws")
	sess := filepath.Join(dir, "sess")
	os.MkdirAll(ws, 0o755)
	os.MkdirAll(sess, 0o755)
	obs := &recordingObserver{}
	tool := NewShellTool()
	tool.Observe(obs)
	ctx := &ToolCtx{Workspace: ws, SessionDir: sess, SessionID: "s1", CallID: "call-1", Cancel: context.Background()}

	done := startJob(t, tool, ctx, `{"cmd":"echo hi","background":true}`)
	if e := waitFinished(t, obs, done); e.Status != JobDone || e.ExitCode == nil || *e.ExitCode != 0 {
		t.Fatalf("done job: %+v", e)
	}

	cancelled := startJob(t, tool, ctx, `{"cmd":"sleep 30","background":true}`)
	if _, err := tool.Call(json.RawMessage(`{"action":"cancel","job_id":"`+cancelled+`"}`), ctx); err != nil {
		t.Fatal(err)
	}
	if e := waitFinished(t, obs, cancelled); e.Status != JobCancelled {
		t.Fatalf("cancelled job: %+v", e)
	}
	// A second cancel does not finalize again.
	tool.Call(json.RawMessage(`{"action":"cancel","job_id":"`+cancelled+`"}`), ctx)

	timedOut := startJob(t, tool, ctx, `{"cmd":"sleep 30","background":true,"timeout_ms":200}`)
	if e := waitFinished(t, obs, timedOut); e.Status != JobTimeout {
		t.Fatalf("timed-out job: %+v", e)
	}

	obs.mu.Lock()
	started := append([]JobEvent(nil), obs.started...)
	obs.mu.Unlock()
	if len(started) != 3 {
		t.Fatalf("started %d", len(started))
	}
	for _, e := range started {
		if e.SessionID != "s1" || e.CallID != "call-1" || e.Workspace != ws || e.SessionDir != sess || e.StartedAt.IsZero() {
			t.Fatalf("start event %+v", e)
		}
	}
	for _, id := range []string{done, cancelled, timedOut} {
		if len(obs.finishedFor(id)) != 1 {
			t.Fatalf("job %s finalized %d times", id, len(obs.finishedFor(id)))
		}
	}
}

func TestShellOutputIsRedacted(t *testing.T) {
	sh := NewShellTool()
	out, err := sh.Call([]byte(`{"cmd":"echo OPENAI_API_KEY=abcdef1234567890xyz; echo ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123"}`), &ToolCtx{Workspace: t.TempDir(), Cancel: context.Background()})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "abcdef1234567890xyz") || strings.Contains(out, "ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123") || !strings.Contains(out, "OPENAI_API_KEY=") {
		t.Fatalf("output %s", out)
	}
}
