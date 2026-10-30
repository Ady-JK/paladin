package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func sleepMs(ms int) {
	deadline := time.Now().Add(time.Duration(ms) * time.Millisecond)
	for time.Now().Before(deadline) {
		select {
		case <-context.Background().Done():
		case <-time.After(time.Millisecond):
		}
	}
}

func afterMs(ms int) <-chan time.Time {
	return time.After(time.Duration(ms) * time.Millisecond)
}

func TestStripANSIAndToLF(t *testing.T) {
	if got := StripANSI("\x1b[31mred\x1b[0m plain"); got != "red plain" {
		t.Fatalf("got %q", got)
	}
	if got := StripANSI("trailing\x1b"); got != "trailing" {
		t.Fatalf("got %q", got)
	}
	if got := ToLF("a\r\nb\rc"); got != "a\nb\nc" {
		t.Fatalf("got %q", got)
	}
	// UTF-8 passes through untouched.
	if got := StripANSI("héllo → 世界"); got != "héllo → 世界" {
		t.Fatalf("got %q", got)
	}
}

func TestSyncRunReturnsOutput(t *testing.T) {
	ctx, _ := testCtx(t)
	tool := NewShellTool()
	out, err := tool.Call(json.RawMessage(`{"cmd":"echo hi"}`), ctx)
	if err != nil {
		t.Fatal(err)
	}
	var resp ShellResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Status != JobDone || resp.Output == nil || *resp.Output != "hi\n" {
		t.Fatalf("got %+v", resp)
	}
}

func TestShellFindsGoWithMinimalPath(t *testing.T) {
	// The sidecar inherits Electron's desktop PATH without the
	// toolchain dirs, so `go` must still resolve (regression: every
	// go/build command failed with exit 127 under the app).
	if _, err := os.Stat("/usr/local/go/bin/go"); err != nil {
		t.Skip("no system go install")
	}
	t.Setenv("PATH", "/usr/bin:/bin")
	ctx, _ := testCtx(t)
	tool := NewShellTool()
	out, err := tool.Call(json.RawMessage(`{"cmd":"go version"}`), ctx)
	if err != nil {
		t.Fatal(err)
	}
	var resp ShellResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Status != JobDone || resp.Output == nil || !strings.Contains(*resp.Output, "go version") {
		t.Fatalf("got %+v", resp)
	}
}

func TestFailingCommandIsToolError(t *testing.T) {
	ctx, _ := testCtx(t)
	tool := NewShellTool()
	_, err := tool.Call(json.RawMessage(`{"cmd":"exit 3"}`), ctx)
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); got != "shell: the command exited with status 3" && len(got) < 10 {
		t.Fatalf("got %q", got)
	}
}

func TestBackgroundStatusCancel(t *testing.T) {
	dir := t.TempDir()
	ws := filepath.Join(dir, "ws")
	sess := filepath.Join(dir, "sess")
	os.MkdirAll(ws, 0o755)
	os.MkdirAll(sess, 0o755)
	ctx := &ToolCtx{Workspace: ws, SessionDir: sess, Cancel: context.Background()}
	tool := NewShellTool()
	out, err := tool.Call(json.RawMessage(`{"cmd":"sleep 30","background":true}`), ctx)
	if err != nil {
		t.Fatal(err)
	}
	var resp ShellResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.JobID == nil {
		t.Fatal("missing job id")
	}
	statusOut, err := tool.Call(json.RawMessage(`{"action":"status","job_id":"`+*resp.JobID+`"}`), ctx)
	if err != nil {
		t.Fatal(err)
	}
	var status ShellResponse
	if err := json.Unmarshal([]byte(statusOut), &status); err != nil {
		t.Fatal(err)
	}
	if status.Status != JobRunning {
		t.Fatalf("got %+v", status)
	}
	cancelOut, err := tool.Call(json.RawMessage(`{"action":"cancel","job_id":"`+*resp.JobID+`"}`), ctx)
	if err != nil {
		t.Fatal(err)
	}
	var cancelled ShellResponse
	if err := json.Unmarshal([]byte(cancelOut), &cancelled); err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != JobCancelled {
		t.Fatalf("got %+v", cancelled)
	}
}

func TestShellValidation(t *testing.T) {
	ctx, ws := testCtx(t)
	tool := NewShellTool()
	for _, args := range []string{
		`{"action":"run"}`,
		`{"action":"run","cmd":""}`,
		`{"action":"run","cmd":"echo hi","timeout_ms":-1}`,
		`{"action":"status"}`,
		`{"action":"cancel"}`,
		`{"action":"bogus"}`,
		`{bad`,
	} {
		if _, err := tool.Call(json.RawMessage(args), ctx); err == nil {
			t.Fatalf("accepted %s", args)
		}
	}
	if _, err := tool.Call(json.RawMessage(`{"action":"status","job_id":"nope"}`), ctx); err == nil {
		t.Fatal("unknown job accepted")
	}
	if _, err := tool.Call(json.RawMessage(`{"action":"cancel","job_id":"nope"}`), ctx); err == nil {
		t.Fatal("unknown cancel accepted")
	}
	// Missing workspace.
	badws := &ToolCtx{Workspace: filepath.Join(ws, "nope"), SessionDir: ctx.SessionDir, Cancel: context.Background()}
	if _, err := tool.Call(json.RawMessage(`{"cmd":"echo hi"}`), badws); err == nil {
		t.Fatal("missing workspace accepted")
	}
	// Background without a session dir.
	nosess := &ToolCtx{Workspace: ctx.Workspace, Cancel: context.Background()}
	if _, err := tool.Call(json.RawMessage(`{"cmd":"echo hi","background":true}`), nosess); err == nil {
		t.Fatal("background without session accepted")
	}
}

func TestShellSyncOutput(t *testing.T) {
	ctx, _ := testCtx(t)
	tool := NewShellTool()
	// stdout + stderr merge, ANSI stripped.
	out, err := tool.Call(json.RawMessage(`{"cmd":"echo out; echo err >&2"}`), ctx)
	if err != nil {
		t.Fatal(err)
	}
	var resp ShellResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Status != JobDone || resp.Output == nil || !strings.Contains(*resp.Output, "out") || !strings.Contains(*resp.Output, "err") {
		t.Fatalf("got %+v", resp)
	}
	// Huge output is capped.
	out, err = tool.Call(json.RawMessage(`{"cmd":"python3 -c \"print('y'*100000)\"","timeout_ms":15000}`), ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Truncated == nil || !*resp.Truncated || len(*resp.Output) > MaxShellOutput+100 {
		t.Fatalf("not capped: %d", len(*resp.Output))
	}
	// Timeout surfaces as an error mentioning the limit.
	_, err = tool.Call(json.RawMessage(`{"cmd":"sleep 5","timeout_ms":200}`), ctx)
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("got %v", err)
	}
	// Cancelled context interrupts.
	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = tool.Call(json.RawMessage(`{"cmd":"sleep 5"}`), &ToolCtx{Workspace: ctx.Workspace, SessionDir: ctx.SessionDir, Cancel: cctx})
	if err == nil || !strings.Contains(err.Error(), "interrupted") {
		t.Fatalf("got %v", err)
	}
}

func TestShellBackgroundLifecycle(t *testing.T) {
	dir := t.TempDir()
	ws := filepath.Join(dir, "ws")
	sess := filepath.Join(dir, "sess")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sess, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := &ToolCtx{Workspace: ws, SessionDir: sess, Cancel: context.Background()}
	tool := NewShellTool()
	out, err := tool.Call(json.RawMessage(`{"cmd":"echo hello-bg","background":true}`), ctx)
	if err != nil {
		t.Fatal(err)
	}
	var started ShellResponse
	if err := json.Unmarshal([]byte(out), &started); err != nil {
		t.Fatal(err)
	}
	if started.JobID == nil || started.Status != JobRunning || started.Log == nil {
		t.Fatalf("got %+v", started)
	}
	// Poll until the waiter records completion (bounded).
	deadline := 200
	for i := 0; i < deadline; i++ {
		statusOut, err := tool.Call(json.RawMessage(`{"action":"status","job_id":"`+*started.JobID+`"}`), ctx)
		if err == nil {
			var st ShellResponse
			if err := json.Unmarshal([]byte(statusOut), &st); err != nil {
				t.Fatal(err)
			}
			if st.Status == JobDone {
				break
			}
		}
		sleepMs(50)
	}
	// Log file holds the output.
	data, err := os.ReadFile(*started.Log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "hello-bg") {
		t.Fatalf("log: %q", data)
	}
	// Notify arming + poll round-trip.
	out2, err := tool.Call(json.RawMessage(`{"cmd":"echo notify-me","background":true,"notify":true}`), ctx)
	if err != nil {
		t.Fatal(err)
	}
	var started2 ShellResponse
	if err := json.Unmarshal([]byte(out2), &started2); err != nil {
		t.Fatal(err)
	}
	tool.ArmNotify("owner-1", "call-1", *started2.JobID)
	if !tool.PendingNotify("owner-1") {
		t.Fatal("pending notify missed")
	}
	waitDone := make(chan bool, 1)
	go func() { waitDone <- tool.WaitPendingNotify("owner-1", context.Background()) }()
	select {
	case v := <-waitDone:
		if !v {
			t.Fatal("wait returned false")
		}
	case <-afterMs(10000):
		t.Fatal("wait hung")
	}
	notices := tool.PollNotify("owner-1")
	if len(notices) != 1 || notices[0].CallID != "call-1" || notices[0].Status != JobDone {
		t.Fatalf("got %+v", notices)
	}
	if tool.PendingNotify("owner-1") {
		t.Fatal("reported job still pending")
	}
	if got := tool.PollNotify("owner-1"); len(got) != 0 {
		t.Fatalf("double report: %+v", got)
	}
	// Cancel a live job.
	out3, err := tool.Call(json.RawMessage(`{"cmd":"sleep 30","background":true}`), ctx)
	if err != nil {
		t.Fatal(err)
	}
	var started3 ShellResponse
	if err := json.Unmarshal([]byte(out3), &started3); err != nil {
		t.Fatal(err)
	}
	cancelled, err := tool.Call(json.RawMessage(`{"action":"cancel","job_id":"`+*started3.JobID+`"}`), ctx)
	if err != nil {
		t.Fatal(err)
	}
	var c ShellResponse
	if err := json.Unmarshal([]byte(cancelled), &c); err != nil {
		t.Fatal(err)
	}
	if c.Status != JobCancelled {
		t.Fatalf("got %+v", c)
	}
	// Failed background job status is an error result.
	out4, err := tool.Call(json.RawMessage(`{"cmd":"exit 2","background":true}`), ctx)
	if err != nil {
		t.Fatal(err)
	}
	var started4 ShellResponse
	if err := json.Unmarshal([]byte(out4), &started4); err != nil {
		t.Fatal(err)
	}
	deadlineHit := true
	for i := 0; i < 200; i++ {
		if _, err := tool.Call(json.RawMessage(`{"action":"status","job_id":"`+*started4.JobID+`"}`), ctx); err != nil {
			deadlineHit = false
			break
		}
		sleepMs(50)
	}
	if deadlineHit {
		t.Fatal("failed job never reported failure")
	}
}

func TestShellDrainsBothStreamsAndPreservesOutputEnds(t *testing.T) {
	ctx, _ := testCtx(t)
	tool := NewShellTool()
	cmd := `python3 -c "import sys; sys.stdout.write('head-'); sys.stdout.flush(); sys.stderr.write('e'*100000); sys.stdout.write(('x'*100000)+'-tail-marker')"`
	out, err := tool.Call(json.RawMessage(`{"cmd":`+jsonQuote(cmd)+`,"timeout_ms":3000}`), ctx)
	if err != nil {
		t.Fatalf("command deadlocked or failed: %v", err)
	}
	var resp ShellResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Output == nil || resp.Truncated == nil || !*resp.Truncated {
		t.Fatalf("expected truncated output, got %+v", resp)
	}
	if len(*resp.Output) > MaxShellOutput+len(shellTruncatedMarker)+8 {
		t.Fatalf("output exceeded cap: %d bytes", len(*resp.Output))
	}
	capture := newShellOutputCapture(MaxShellOutput)
	_, _ = capture.Write([]byte("head-" + strings.Repeat("界", MaxShellOutput) + "-tail-marker"))
	bytes, wasTruncated := capture.result()
	if !utf8.Valid(bytes) {
		t.Fatal("captured output split a UTF-8 character")
	}
	code := 0
	encoded, failure := finishSync(bytes, wasTruncated, waitResult{code: &code}, false)
	if failure != nil {
		t.Fatal(failure)
	}
	if err := json.Unmarshal([]byte(encoded), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Output == nil || !strings.HasPrefix(*resp.Output, "head-") || !strings.Contains(*resp.Output, "-tail-marker") {
		t.Fatalf("truncated output must preserve head and tail: %q", *resp.Output)
	}
}

func TestShellDefaultTimeouts(t *testing.T) {
	if d, _ := shellTimeoutMs(0, false); d != 10*time.Second {
		t.Fatalf("foreground default %v", d)
	}
	if d, _ := shellTimeoutMs(0, true); d != 300*time.Second {
		t.Fatalf("background default %v", d)
	}
	if d, _ := shellTimeoutMs(2000, true); d != 2*time.Second {
		t.Fatalf("explicit timeout ignored: %v", d)
	}
}

func TestShellTimeoutOverflowRejected(t *testing.T) {
	if _, err := shellTimeoutMs(int64(^uint64(0)>>1), false); err == nil {
		t.Fatal("overflowing timeout accepted")
	}
}

func TestShellRunRejectsNilContextWithoutPanicking(t *testing.T) {
	_, err := NewShellTool().Call(json.RawMessage(`{"cmd":"echo hi"}`), nil)
	if err == nil || !strings.Contains(err.Error(), "no workspace") {
		t.Fatalf("expected a workspace validation error, got %v", err)
	}
}
