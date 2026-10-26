package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestToolErrorStrings(t *testing.T) {
	cases := []struct {
		err  *ToolError
		want string
	}{
		{&ToolError{Kind: "unknown", Msg: "x"}, "unknown tool: x"},
		{&ToolError{Kind: "args", Msg: "bad"}, "bad arguments: bad"},
		{&ToolError{Kind: "exec", Msg: "fail"}, "execution failed: fail"},
		{&ToolError{Kind: "failed", Msg: "m", Content: "c"}, "m"},
		{&ToolError{Kind: "other", Msg: "m"}, "m"},
	}
	for _, tc := range cases {
		if got := tc.err.Error(); got != tc.want {
			t.Errorf("got %q want %q", got, tc.want)
		}
	}
	if got := WithContent("c", "m").ResultContent(); got != "c" {
		t.Fatalf("got %q", got)
	}
	if got := ArgsError("bad").ResultContent(); !strings.Contains(got, "bad") {
		t.Fatalf("got %q", got)
	}
	plain := &ToolError{Kind: "args", Msg: "bad"}
	if got := plain.ResultContent(); !strings.Contains(got, "bad arguments") {
		t.Fatalf("got %q", got)
	}
	// Non-ToolError results are wrapped as JSON.
	r := NewRegistry()
	r.Register(ReadTool{})
	res := r.Call(&ToolCall{ID: "1", Name: "read", Arguments: "{"}, &ToolCtx{})
	if !res.IsError || !strings.Contains(res.Content, "valid JSON") {
		t.Fatalf("got %+v", res)
	}
	// Blank arguments become null -> read complains about path.
	res = r.Call(&ToolCall{ID: "1", Name: "read", Arguments: "   "}, &ToolCtx{Workspace: t.TempDir()})
	if !res.IsError {
		t.Fatalf("got %+v", res)
	}
}

func TestRegistryDefinitionsSorted(t *testing.T) {
	r := NewRegistry()
	r.Register(NewShellTool())
	r.Register(ReadTool{})
	defs := r.Definitions()
	if len(defs) != 2 || defs[0].Name != "read" || defs[1].Name != "shell" {
		t.Fatalf("got %v", defs)
	}
	if got := NewRegistry().Definitions(); got == nil || len(got) != 0 {
		t.Fatalf("empty registry should give [], got %+v", got)
	}
	if _, ok := r.Get("read"); !ok {
		t.Fatal("Get failed")
	}
	if _, ok := r.Get("missing"); ok {
		t.Fatal("Get false positive")
	}
}

func TestScopeMatrix(t *testing.T) {
	ws := t.TempDir()
	// Empty path.
	if _, _, err := ResolveScopedPath("read", "path", "   ", ws); err == nil {
		t.Fatal("empty accepted")
	}
	// No workspace.
	if _, _, err := ResolveScopedPath("read", "path", "workspace/a", ""); err == nil {
		t.Fatal("missing workspace accepted")
	}
	// Escape via ..
	if _, _, err := ResolveScopedPath("read", "path", "workspace/../etc/hosts", ws); err == nil {
		t.Fatal("escape accepted")
	}
	// Bare relative rejected, absolute accepted.
	if _, _, err := ResolveScopedPath("read", "path", "a/b", ws); err == nil {
		t.Fatal("bare relative accepted")
	}
	p, scope, err := ResolveScopedPath("read", "path", "/etc/hosts", ws)
	if err != nil || scope != ScopeOutside || p != "/etc/hosts" {
		t.Fatalf("got %q %q %v", p, scope, err)
	}
	// workspace/ prefix alone resolves to the root.
	p, scope, err = ResolveScopedPath("read", "path", "workspace", ws)
	if err != nil || scope != ScopeInside || p != ws {
		t.Fatalf("got %q %q %v", p, scope, err)
	}
	// Backslash prefix works.
	p, _, err = ResolveScopedPath("read", "path", `workspace\sub\a.txt`, ws)
	if err != nil || !strings.HasSuffix(p, filepath.Join("sub", "a.txt")) {
		t.Fatalf("got %q %v", p, err)
	}
	// Symlink escape is rejected: link inside pointing outside.
	outside := t.TempDir()
	link := filepath.Join(ws, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ResolveScopedPath("read", "path", "workspace/link/secret", ws); err == nil {
		t.Fatal("symlink escape accepted")
	}
	// cleanPath lexically resolves dot segments.
	if got := cleanPath("/a/./b/../c"); got != "/a/c" {
		t.Fatalf("got %q", got)
	}
}

func TestReadRangesAndCaps(t *testing.T) {
	ctx, ws := testCtx(t)
	// Offset past EOF: empty content, start/end 0.
	if err := os.WriteFile(filepath.Join(ws, "a.txt"), []byte("l1\nl2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := (ReadTool{}).Call(json.RawMessage(`{"path":"workspace/a.txt","offset":99}`), ctx)
	if err != nil {
		t.Fatal(err)
	}
	var resp ReadResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Content != "" || resp.HasMore || resp.StartLine != 0 {
		t.Fatalf("got %+v", resp)
	}
	// No trailing newline on last line.
	if err := os.WriteFile(filepath.Join(ws, "b.txt"), []byte("x\ny"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = (ReadTool{}).Call(json.RawMessage(`{"path":"workspace/b.txt"}`), ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Content != "x\ny\n" || resp.EndLine != 2 {
		t.Fatalf("got %+v", resp)
	}
	// Byte cap triggers before line cap: many medium lines exceed 50K.
	var sb strings.Builder
	for i := 0; i < 100; i++ {
		sb.WriteString(strings.Repeat("z", 1000))
		sb.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(ws, "big.txt"), []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = (ReadTool{}).Call(json.RawMessage(`{"path":"workspace/big.txt","limit":2000}`), ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.HasMore || resp.StopReason != "byte_limit" {
		t.Fatalf("got %+v", resp)
	}
	// Long lines are truncated with a marker.
	long := strings.Repeat("a", 3000)
	if err := os.WriteFile(filepath.Join(ws, "long.txt"), []byte(long+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = (ReadTool{}).Call(json.RawMessage(`{"path":"workspace/long.txt"}`), ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(strings.TrimRight(resp.Content, "\n"), "… [truncated]") {
		t.Fatalf("no truncation marker: %q...", resp.Content[:80])
	}
	// Missing file, directory, negative args.
	if _, err := (ReadTool{}).Call(json.RawMessage(`{"path":"workspace/nope.txt"}`), ctx); err == nil {
		t.Fatal("missing accepted")
	}
	if _, err := (ReadTool{}).Call(json.RawMessage(`{"path":"workspace"}`), ctx); err == nil {
		t.Fatal("directory accepted")
	}
	if _, err := (ReadTool{}).Call(json.RawMessage(`{"path":"workspace/a.txt","offset":-1}`), ctx); err == nil {
		t.Fatal("negative offset accepted")
	}
	if _, err := (ReadTool{}).Call(json.RawMessage(`{"path":"workspace/a.txt","limit":-2}`), ctx); err == nil {
		t.Fatal("negative limit accepted")
	}
	if _, err := (ReadTool{}).Call(json.RawMessage(`{bad`), ctx); err == nil {
		t.Fatal("bad json accepted")
	}
	// CRLF normalizes to LF.
	if err := os.WriteFile(filepath.Join(ws, "crlf.txt"), []byte("a\r\nb\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = (ReadTool{}).Call(json.RawMessage(`{"path":"workspace/crlf.txt"}`), ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Content != "a\nb\n" {
		t.Fatalf("got %q", resp.Content)
	}
}

func TestWriteEngineEdges(t *testing.T) {
	dir := t.TempDir()
	ws := filepath.Join(dir, "ws")
	engine := NewEngine()
	// Empty path.
	if _, err := engine.Write(ws, "  ", "x", false); err == nil {
		t.Fatal("empty path accepted")
	}
	// Write through an existing directory path.
	if err := os.MkdirAll(filepath.Join(ws, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Write(ws, "workspace/d", "x", false); err == nil {
		t.Fatal("directory write accepted")
	}
	// Nested parents are created.
	res, err := engine.Write(ws, "workspace/a/b/c.txt", "hi\nthere\n", false)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Created || res.Overwritten || res.Lines != 2 || res.Bytes != 9 {
		t.Fatalf("got %+v", res)
	}
	// Overwrite replaces.
	res, err = engine.Write(ws, "workspace/a/b/c.txt", "new", true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Created || !res.Overwritten {
		t.Fatalf("got %+v", res)
	}
	// Tool-level clobber error carries exists:true.
	ctx := &ToolCtx{Workspace: ws, SessionDir: dir, Cancel: context.Background()}
	tool := NewWriteTool(engine)
	if _, err := tool.Call(json.RawMessage(`{"path":"workspace/a/b/c.txt","content":"x"}`), ctx); err == nil {
		t.Fatal("clobber accepted")
	}
	if _, err := tool.Call(json.RawMessage(`{"path":"","content":"x"}`), ctx); err == nil {
		t.Fatal("empty path accepted")
	}
}

func TestEditEngineEdges(t *testing.T) {
	dir := t.TempDir()
	ws := filepath.Join(dir, "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	engine := NewEngine()
	// Missing file.
	if _, err := engine.Edit(ws, "workspace/nope.txt", "a", "b", false); err == nil {
		t.Fatal("missing accepted")
	}
	// Binary rejected.
	if err := os.WriteFile(filepath.Join(ws, "bin"), []byte{0, 1, 2}, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Edit(ws, "workspace/bin", "a", "b", false); err == nil {
		t.Fatal("binary accepted")
	}
	// Not found text.
	if _, err := engine.Write(ws, "workspace/f.txt", "hello\n", false); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Edit(ws, "workspace/f.txt", "nope", "b", false); err == nil {
		t.Fatal("missing text accepted")
	}
	// replace_all replaces every occurrence.
	res, err := engine.Edit(ws, "workspace/f.txt", "l", "L", true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Replacements != 2 || len(res.Matches) != 2 {
		t.Fatalf("got %+v", res)
	}
	// Tool-level notfound and ambiguous carry structured content.
	tool := NewEditTool(engine)
	if _, err := tool.Call(json.RawMessage(`{"path":"workspace/f.txt","old_string":"zzz","new_string":"y"}`), ctx2(dir, ws)); err == nil {
		t.Fatal("notfound accepted")
	} else if terr, ok := err.(*ToolError); !ok || terr.Content == "" {
		t.Fatalf("expected content-carrying error, got %v", err)
	}
	if _, err := engine.Write(ws, "workspace/g.txt", "x\nx\n", false); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Call(json.RawMessage(`{"path":"workspace/g.txt","old_string":"x","new_string":"y"}`), ctx2(dir, ws)); err == nil {
		t.Fatal("ambiguous accepted")
	} else if terr, ok := err.(*ToolError); !ok || !strings.Contains(terr.Content, "occurrences") {
		t.Fatalf("expected occurrences payload, got %v", err)
	}
	// Over-limit file rejected.
	big := make([]byte, MaxFileBytes+1)
	if err := os.WriteFile(filepath.Join(ws, "huge.txt"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Edit(ws, "workspace/huge.txt", "a", "b", false); err == nil {
		t.Fatal("huge file accepted")
	}
}

func ctx2(dir, ws string) *ToolCtx {
	return &ToolCtx{Workspace: ws, SessionDir: dir, Cancel: context.Background()}
}

func TestFindOccurrencesContext(t *testing.T) {
	content := "aaa\n\nbbb xxx\n\nccc xxx ddd\n"
	occ := FindOccurrences(content, "xxx")
	if len(occ) != 2 {
		t.Fatalf("got %v", occ)
	}
	if occ[0].StartLine != 3 || occ[1].StartLine != 5 {
		t.Fatalf("got %+v", occ)
	}
	if occ[0].Before == nil || occ[0].Before.Text != "aaa" {
		t.Fatalf("before: %+v", occ[0].Before)
	}
	if occ[0].After == nil || occ[0].After.Text != "ccc xxx ddd" {
		t.Fatalf("after: %+v", occ[0].After)
	}
	if got := CountLines(""); got != 0 {
		t.Fatalf("got %d", got)
	}
	if got := CountLines("a\nb"); got != 2 {
		t.Fatalf("got %d", got)
	}
	if got := CountLines("a\nb\n"); got != 2 {
		t.Fatalf("got %d", got)
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

func TestShellValidation(t *testing.T) {
	ctx, _ := testCtx(t)
	tool := NewShellTool()
	for _, args := range []string{
		`{"action":"run"}`,
		`{"action":"run","cmd":"","workdir":"workspace"}`,
		`{"action":"run","cmd":"echo hi","timeout_ms":-1}`,
		`{"action":"run","cmd":"echo hi","workdir":"relative"}`,
		`{"action":"status"}`,
		`{"action":"cancel"}`,
		`{"action":"bogus"}`,
		`{bad`,
		`{"action":"run","cmd":"echo hi","workdir":"workspace/../etc"}`,
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
	// Missing workdir.
	if _, err := tool.Call(json.RawMessage(`{"cmd":"echo hi","workdir":"workspace/nope"}`), ctx); err == nil {
		t.Fatal("missing workdir accepted")
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
