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

func TestShellCommandParamAndLegacyCmd(t *testing.T) {
	ctx, _ := testCtx(t)
	tool := NewShellTool()
	cases := []struct {
		name string
		args string
		want string
	}{
		{"command", `{"command":"echo new"}`, "new\n"},
		{"legacy cmd", `{"cmd":"echo old"}`, "old\n"},
		{"command wins", `{"command":"echo new","cmd":"echo old"}`, "new\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := tool.Call(json.RawMessage(tc.args), ctx)
			if err != nil {
				t.Fatal(err)
			}
			var resp ShellResponse
			if err := json.Unmarshal([]byte(out), &resp); err != nil {
				t.Fatal(err)
			}
			if resp.Output == nil || *resp.Output != tc.want {
				t.Fatalf("got %+v, want output %q", resp, tc.want)
			}
		})
	}
}

func TestShellDefinitionAdvertisesCommand(t *testing.T) {
	def := NewShellTool().Definition()
	params, ok := def.Parameters.(map[string]any)
	if !ok {
		t.Fatalf("parameters are not an object: %#v", def.Parameters)
	}
	props, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatalf("parameters have no properties: %#v", params)
	}
	if _, ok := props["command"]; !ok {
		t.Fatal("shell definition does not advertise command")
	}
	if _, ok := props["cmd"]; ok {
		t.Fatal("shell definition still advertises the legacy cmd name")
	}
}

func TestShellRegistryAcceptsBothNames(t *testing.T) {
	ctx, _ := testCtx(t)
	reg := BuiltinRegistry()
	for _, tc := range []struct {
		name string
		args string
		want string
	}{
		{"command", `{"command":"echo new"}`, "new\n"},
		{"legacy cmd", `{"cmd":"echo old"}`, "old\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := reg.Call(&ToolCall{ID: "c1", Name: "shell", Arguments: tc.args}, ctx)
			if res.IsError {
				t.Fatalf("call failed: %s", res.Content)
			}
			var resp ShellResponse
			if err := json.Unmarshal([]byte(res.Content), &resp); err != nil {
				t.Fatal(err)
			}
			if resp.Output == nil || *resp.Output != tc.want {
				t.Fatalf("got %+v, want output %q", resp, tc.want)
			}
		})
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
	out, err := tool.Call(json.RawMessage(`{"command":"sleep 30","background":true}`), ctx)
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
		`{"action":"run","command":""}`,
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
	if _, err := tool.Call(json.RawMessage(`{"action":"run"}`), ctx); err == nil || !strings.Contains(err.Error(), "command is required") {
		t.Fatalf("missing command error = %v", err)
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
	out, err = tool.Call(json.RawMessage(`{"cmd":"python3 -c \"print('y'*100000)\""}`), ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Truncated == nil || !*resp.Truncated || len(*resp.Output) > MaxShellOutput+100 {
		t.Fatalf("not capped: %d", len(*resp.Output))
	}
	// Cancelled context interrupts.
	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = tool.Call(json.RawMessage(`{"cmd":"sleep 5"}`), &ToolCtx{Workspace: ctx.Workspace, SessionDir: ctx.SessionDir, Cancel: cctx})
	if err == nil || !strings.Contains(err.Error(), "interrupted") {
		t.Fatalf("got %v", err)
	}
}

func TestShellLongCommandMovesToBackground(t *testing.T) {
	ctx, _ := testCtx(t)
	old := DefaultShellYieldMs
	DefaultShellYieldMs = 100
	defer func() { DefaultShellYieldMs = old }()
	tool := NewShellTool()
	out, err := tool.Call(json.RawMessage(`{"cmd":"sleep 5"}`), ctx)
	if err != nil {
		t.Fatal(err)
	}
	var resp ShellResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Status != JobRunning || resp.JobID == nil || resp.Log == nil || resp.Note == nil {
		t.Fatalf("got %+v", resp)
	}
	if !strings.Contains(*resp.Note, "background") {
		t.Fatalf("note: %q", *resp.Note)
	}
	if _, err := tool.Call(json.RawMessage(`{"action":"cancel","job_id":"`+*resp.JobID+`"}`), ctx); err != nil {
		t.Fatal(err)
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
	// notify is off here so the notify round-trip below sees only its own job.
	out, err := tool.Call(json.RawMessage(`{"cmd":"echo hello-bg","background":true,"notify":false}`), ctx)
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
	// Notify round-trip: the job reports itself once when it finishes.
	ctx.CallID = "call-2"
	out2, err := tool.Call(json.RawMessage(`{"cmd":"echo notify-me","background":true,"notify":true}`), ctx)
	if err != nil {
		t.Fatal(err)
	}
	var started2 ShellResponse
	if err := json.Unmarshal([]byte(out2), &started2); err != nil {
		t.Fatal(err)
	}
	var notices []NotifyResult
	for i := 0; i < 200; i++ {
		notices = tool.PollNotify("")
		if len(notices) > 0 {
			break
		}
		sleepMs(50)
	}
	if len(notices) != 1 || notices[0].JobID != *started2.JobID || notices[0].Status != JobDone || notices[0].CallID != "call-2" {
		t.Fatalf("got %+v", notices)
	}
	if got := tool.PollNotify(""); len(got) != 0 {
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
	out, err := tool.Call(json.RawMessage(`{"cmd":`+jsonQuote(cmd)+`}`), ctx)
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
	path := filepath.Join(t.TempDir(), "log.txt")
	text := "head-" + strings.Repeat("界", MaxShellOutput) + "-tail-marker"
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	bytes, wasTruncated, err := readJobOutput(path, MaxShellOutput)
	if err != nil {
		t.Fatal(err)
	}
	bytes = []byte(strings.ToValidUTF8(string(bytes), "�"))
	if !utf8.Valid(bytes) {
		t.Fatal("captured output split a UTF-8 character")
	}
	if !wasTruncated {
		t.Fatal("oversized output was not truncated")
	}
	code := 0
	encoded, failure := finishSync(bytes, wasTruncated, path, waitResult{code: &code}, false)
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

func TestShellRunRejectsNilContextWithoutPanicking(t *testing.T) {
	_, err := NewShellTool().Call(json.RawMessage(`{"cmd":"echo hi"}`), nil)
	if err == nil || !strings.Contains(err.Error(), "no workspace") {
		t.Fatalf("expected a workspace validation error, got %v", err)
	}
}
