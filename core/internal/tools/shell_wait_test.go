package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
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

func TestShellJobIDsAreShortAndDoNotOverwriteOldLogs(t *testing.T) {
	ctx, _ := testCtx(t)
	tmp := filepath.Join(ctx.SessionDir, "tmp")
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(tmp, "job-1.log")
	if err := os.WriteFile(old, []byte("old output"), 0o600); err != nil {
		t.Fatal(err)
	}
	tool := NewShellTool()
	id := startBackground(t, tool, ctx, `{"command":"echo done","background":true}`)
	if id != "job-2" {
		t.Fatalf("job id = %q, want job-2", id)
	}
	if _, err := tool.Call(json.RawMessage(`{"action":"wait","job_id":"`+id+`","seconds":5}`), ctx); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(old)
	if err != nil || string(data) != "old output" {
		t.Fatalf("old log = %q, %v", data, err)
	}
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

// A wait that times out carries the last few lines of the job's output,
// so the model can tell a working command from a stuck one without
// spending another wait on it.
func TestShellWaitTimeoutIncludesOutputTail(t *testing.T) {
	ctx, _ := testCtx(t)
	tool := NewShellTool()
	id := startBackground(t, tool, ctx,
		`{"command":"echo one; echo two; echo three; echo four; echo five; echo six; sleep 30","background":true}`)
	defer func() {
		_, _ = tool.Call(json.RawMessage(`{"action":"cancel","job_id":"`+id+`"}`), ctx)
	}()
	// Wait for the echoes to land before waiting on the job.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		out, _ := tool.Call(json.RawMessage(`{"action":"status","job_id":"`+id+`"}`), ctx)
		var peek ShellResponse
		_ = json.Unmarshal([]byte(out), &peek)
		if peek.Output != nil && strings.Contains(*peek.Output, "six") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	out, err := tool.Call(json.RawMessage(`{"action":"wait","job_id":"`+id+`","seconds":1}`), ctx)
	if err != nil {
		t.Fatal(err)
	}
	var resp ShellResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Status != JobRunning {
		t.Fatalf("status = %q, want running", resp.Status)
	}
	if resp.Output == nil {
		t.Fatalf("running answer carried no output peek: %+v", resp)
	}
	want := "two\nthree\nfour\nfive\nsix"
	if *resp.Output != want {
		t.Fatalf("peek = %q, want %q", *resp.Output, want)
	}
	// The note still tells the model what to do next.
	if resp.Note == nil || !strings.Contains(*resp.Note, "wait again") {
		t.Fatalf("note = %v", resp.Note)
	}
}

// A job that has printed nothing yet carries no peek rather than an
// empty string.
func TestShellWaitTimeoutWithoutOutput(t *testing.T) {
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
	if resp.Status != JobRunning {
		t.Fatalf("status = %q", resp.Status)
	}
	if resp.Output != nil {
		t.Fatalf("expected no peek, got %q", *resp.Output)
	}
	if resp.Note == nil || !strings.Contains(*resp.Note, "wait again") {
		t.Fatalf("note = %v", resp.Note)
	}
}

// peekTail reads a bounded window from the log's end, so a job that has
// printed megabytes still yields exactly the last lines.
func TestPeekTailBoundsLargeLog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.log")
	var buf strings.Builder
	for i := 0; i < 50000; i++ {
		buf.WriteString("line-" + strconv.Itoa(i) + "\n")
	}
	if err := os.WriteFile(path, []byte(buf.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	lines, omitted := peekTail(path)
	if len(lines) != RunningPeekLines {
		t.Fatalf("got %d lines, want %d: %v", len(lines), RunningPeekLines, lines)
	}
	if !omitted {
		t.Fatal("a 50k-line log should report earlier output omitted")
	}
	if want := "line-49999"; lines[len(lines)-1] != want {
		t.Fatalf("last line = %q, want %q", lines[len(lines)-1], want)
	}
	// Every line shown must be whole, never a fragment of a longer one.
	for _, line := range lines {
		if !strings.HasPrefix(line, "line-") {
			t.Fatalf("partial line in the peek: %q", line)
		}
	}
}

// A log with fewer lines than the peek asks for is returned whole, with
// nothing claimed as omitted.
func TestPeekTailShortLog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "small.log")
	if err := os.WriteFile(path, []byte("only one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lines, omitted := peekTail(path)
	if omitted {
		t.Fatal("nothing was omitted from a one-line log")
	}
	if len(lines) != 1 || lines[0] != "only one" {
		t.Fatalf("got %v", lines)
	}
}

// An empty or missing log yields no peek at all.
func TestPeekTailEmptyAndMissing(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.log")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if lines, _ := peekTail(empty); len(lines) != 0 {
		t.Fatalf("empty log gave %v", lines)
	}
	if lines, _ := peekTail(filepath.Join(dir, "nope.log")); len(lines) != 0 {
		t.Fatalf("missing log gave %v", lines)
	}
}
