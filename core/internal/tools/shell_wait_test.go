package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

func startBackground(t *testing.T, tool *ShellTool, ctx *ToolCtx, args string) string {
	t.Helper()
	out, err := tool.Call(json.RawMessage(args), ctx)
	if err != nil {
		t.Fatal(err)
	}
	var resp ShellResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.JobID == nil || resp.Status != JobRunning {
		t.Fatalf("expected a running job, got %+v", resp)
	}
	return *resp.JobID
}

// A wait on a job that ends during the call returns its result like a
// foreground run would, and does not leave a completion notice behind.
func TestShellWaitReturnsFinishedResult(t *testing.T) {
	ctx, _ := testCtx(t)
	tool := NewShellTool()
	id := startBackground(t, tool, ctx, `{"command":"sleep 0.2; echo waited-output","background":true}`)

	out, err := tool.Call(json.RawMessage(`{"action":"wait","job_id":"`+id+`","seconds":5}`), ctx)
	if err != nil {
		t.Fatal(err)
	}
	var resp ShellResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Status != JobDone || resp.Output == nil || !strings.Contains(*resp.Output, "waited-output") {
		t.Fatalf("got %+v", resp)
	}
	if got := tool.PollNotify(""); len(got) != 0 {
		t.Fatalf("wait should consume the notice, got %d", len(got))
	}
}

// A wait that runs out reports the job as still running and says to wait
// again, without killing anything.
func TestShellWaitTimesOutWhileRunning(t *testing.T) {
	ctx, _ := testCtx(t)
	tool := NewShellTool()
	id := startBackground(t, tool, ctx, `{"command":"sleep 30","background":true}`)
	defer func() {
		_, _ = tool.Call(json.RawMessage(`{"action":"cancel","job_id":"`+id+`"}`), ctx)
	}()

	out, err := tool.Call(json.RawMessage(`{"action":"wait","job_id":"`+id+`","seconds":1}`), ctx)
	if err != nil {
		t.Fatal(err)
	}
	var resp ShellResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Status != JobRunning || resp.Note == nil || !strings.Contains(*resp.Note, "wait again") {
		t.Fatalf("got %+v", resp)
	}
}

func TestShellWaitUnknownJob(t *testing.T) {
	ctx, _ := testCtx(t)
	tool := NewShellTool()
	_, err := tool.Call(json.RawMessage(`{"action":"wait","job_id":"job-nope"}`), ctx)
	if err == nil || !strings.Contains(err.Error(), "unknown job") {
		t.Fatalf("got %v", err)
	}
}

func TestWaitSecondsClamp(t *testing.T) {
	cases := []struct{ in, want int }{
		{0, DefaultShellWaitSeconds},
		{-5, DefaultShellWaitSeconds},
		{120, 120},
		{MaxShellWaitSeconds + 1, MaxShellWaitSeconds},
	}
	for _, c := range cases {
		if got := waitSeconds(c.in); got != c.want {
			t.Fatalf("waitSeconds(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

// A background job reports its completion unless the caller opts out.
func TestShellBackgroundNotifiesByDefault(t *testing.T) {
	ctx, _ := testCtx(t)
	tool := NewShellTool()
	id := startBackground(t, tool, ctx, `{"command":"echo notify-default","background":true}`)
	waitForJobEnd(t, tool, ctx, id)

	notices := tool.PollNotify("")
	if len(notices) != 1 || notices[0].Status != JobDone {
		t.Fatalf("got %+v", notices)
	}
	if got := tool.PollNotify(""); len(got) != 0 {
		t.Fatalf("notice delivered twice: %d", len(got))
	}
}

func TestShellBackgroundNotifyCanBeDisabled(t *testing.T) {
	ctx, _ := testCtx(t)
	tool := NewShellTool()
	id := startBackground(t, tool, ctx, `{"command":"echo silent","background":true,"notify":false}`)
	waitForJobEnd(t, tool, ctx, id)

	if got := tool.PollNotify(""); len(got) != 0 {
		t.Fatalf("expected no notice, got %+v", got)
	}
}

// waitForJobEnd blocks until status stops reporting the job as running,
// without consuming any notice.
func waitForJobEnd(t *testing.T, tool *ShellTool, ctx *ToolCtx, id string) {
	t.Helper()
	for i := 0; i < 400; i++ {
		out, err := tool.Call(json.RawMessage(`{"action":"status","job_id":"`+id+`"}`), ctx)
		if err != nil {
			return // a failed job's status returns its body as an error
		}
		var resp ShellResponse
		if json.Unmarshal([]byte(out), &resp) == nil && resp.Status != JobRunning {
			return
		}
		sleepMs(5)
	}
	t.Fatalf("job %s did not end", id)
}
