package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
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
	waitForJobEnd(t, tool, ctx, id)
	data, err := os.ReadFile(old)
	if err != nil || string(data) != "old output" {
		t.Fatalf("old log = %q, %v", data, err)
	}
}

// The wait action is gone: a model that still calls it (an old habit, an
// old transcript) gets an error that says what to do instead, and the job
// it named is left alone.
func TestShellWaitActionWasRemoved(t *testing.T) {
	ctx, _ := testCtx(t)
	tool := NewShellTool()
	id := startBackground(t, tool, ctx, `{"command":"sleep 30","background":true}`)
	defer func() {
		_, _ = tool.Call(json.RawMessage(`{"action":"cancel","job_id":"`+id+`"}`), ctx)
	}()

	for _, args := range []string{
		`{"action":"wait","job_id":"` + id + `","seconds":5}`,
		`{"action":"wait"}`,
		`{"action":"wait","job_id":"job-nope"}`,
	} {
		_, err := tool.Call(json.RawMessage(args), ctx)
		if err == nil {
			t.Fatalf("%s: wait should be refused", args)
		}
		for _, want := range []string{"wait action was removed", "end your turn", "cancel"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("%s: error %q lacks %q", args, err, want)
			}
		}
	}

	out, err := tool.Call(json.RawMessage(`{"action":"status","job_id":"`+id+`"}`), ctx)
	if err != nil {
		t.Fatal(err)
	}
	var resp ShellResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil || resp.Status != JobRunning {
		t.Fatalf("the refused wait disturbed the job: %s (%v)", out, err)
	}
}

func TestShellActionErrorDoesNotOfferWait(t *testing.T) {
	ctx, _ := testCtx(t)
	_, err := NewShellTool().Call(json.RawMessage(`{"action":"bogus"}`), ctx)
	if err == nil || !strings.Contains(err.Error(), "run, status or cancel") || strings.Contains(err.Error(), "wait") {
		t.Fatalf("got %v", err)
	}
}

func TestShellDefinitionHasNoWaitAction(t *testing.T) {
	def := NewShellTool().Definition()
	params, ok := def.Parameters.(map[string]any)
	if !ok {
		t.Fatalf("parameters are %T, want a schema map", def.Parameters)
	}
	props := params["properties"].(map[string]any)
	enum := props["action"].(map[string]any)["enum"].([]string)
	for _, a := range enum {
		if a == "wait" {
			t.Fatalf("the action enum still offers wait: %v", enum)
		}
	}
	if _, ok := props["seconds"]; ok {
		t.Fatal("the seconds argument belonged to wait and should be gone")
	}
	for _, want := range []string{"two minutes", "woken", "10 minutes", "30 minutes"} {
		if !strings.Contains(def.Description, want) {
			t.Fatalf("description lacks %q:\n%s", want, def.Description)
		}
	}
	if strings.Contains(def.Description, "wait blocks") || strings.Contains(def.Description, "five minutes") {
		t.Fatalf("description still describes wait or the old five-minute reminder:\n%s", def.Description)
	}
}

// A foreground command that outlives its wait is handed to the background
// and told not to poll: the note must not point at the removed wait action.
func TestShellForegroundHandoffDoesNotPointAtWait(t *testing.T) {
	ctx, _ := testCtx(t)
	tool := NewShellTool()
	old := DefaultShellYieldMs
	DefaultShellYieldMs = 300
	defer func() { DefaultShellYieldMs = old }()

	out, err := tool.Call(json.RawMessage(`{"command":"sleep 30"}`), ctx)
	if err != nil {
		t.Fatal(err)
	}
	var resp ShellResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if resp.JobID != nil {
			_, _ = tool.Call(json.RawMessage(`{"action":"cancel","job_id":"`+*resp.JobID+`"}`), ctx)
		}
	}()
	if resp.Status != JobRunning || resp.Note == nil {
		t.Fatalf("got %+v", resp)
	}
	if !strings.Contains(*resp.Note, "woken when it finishes") || strings.Contains(*resp.Note, "call wait") {
		t.Fatalf("note = %q", *resp.Note)
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

// The runtime watches a job's log by path, so the path it computes must be
// the one the shell tool actually wrote to.
func TestJobLogPathIsTheFileTheJobWrites(t *testing.T) {
	ctx, _ := testCtx(t)
	tool := NewShellTool()
	out, err := tool.Call(json.RawMessage(`{"command":"echo hello-log","background":true}`), ctx)
	if err != nil {
		t.Fatal(err)
	}
	var resp ShellResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil || resp.JobID == nil || resp.Log == nil {
		t.Fatalf("got %s (%v)", out, err)
	}
	if want := JobLogPath(ctx.SessionDir, *resp.JobID); *resp.Log != want {
		t.Fatalf("log = %q, want %q", *resp.Log, want)
	}
	waitForJobEnd(t, tool, ctx, *resp.JobID)
	if LogSize(*resp.Log) == 0 || LogTail(*resp.Log) != "hello-log" {
		t.Fatalf("size %d, tail %q", LogSize(*resp.Log), LogTail(*resp.Log))
	}
}

// LogTail gives the model the last five lines, free of colour codes.
func TestLogTailIsTheLastFiveCleanLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "job.log")
	text := "one\ntwo\n\x1b[31mthree\x1b[0m\nfour\nfive\nsix\nseven\n"
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, want := LogTail(path), "three\nfour\nfive\nsix\nseven"; got != want {
		t.Fatalf("tail = %q, want %q", got, want)
	}
	if got := LogSize(path); got != int64(len(text)) {
		t.Fatalf("size = %d, want %d", got, len(text))
	}
}

func TestLogTailAndSizeOfMissingOrEmptyLog(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.log")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{empty, filepath.Join(dir, "nope.log")} {
		if LogTail(path) != "" || LogSize(path) != 0 {
			t.Fatalf("%s: tail %q size %d", path, LogTail(path), LogSize(path))
		}
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
