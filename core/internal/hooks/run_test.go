//go:build unix

package hooks

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func script(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "hook.sh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func engine(ev Event, matcher string, cmds ...string) *Engine {
	var hs []Handler
	for _, c := range cmds {
		hs = append(hs, Handler{Command: c})
	}
	m, _ := newMatcher(matcher)
	return &Engine{groups: map[Event][]activeGroup{ev: {{matcher: m, handlers: hs}}}}
}

func TestNilEngineIsInert(t *testing.T) {
	var e *Engine
	if e.Has(Stop) {
		t.Fatal("nil engine has no hooks")
	}
	if out := e.Run(context.Background(), Input{Event: Stop}); out.Blocked || len(out.Notes) != 0 {
		t.Fatalf("out = %+v", out)
	}
}

func TestStdinCarriesSnakeCaseInput(t *testing.T) {
	dir := t.TempDir()
	dump := filepath.Join(dir, "in.json")
	e := engine(PreToolUse, "Bash", "cat > "+dump)
	e.Run(context.Background(), Input{Event: PreToolUse, SessionID: "s1", Cwd: dir, ToolName: "shell", ToolInput: json.RawMessage(`{"command":"ls"}`), ToolUseID: "c1"})
	data, err := os.ReadFile(dump)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got["hook_event_name"] != "PreToolUse" || got["session_id"] != "s1" || got["tool_name"] != "shell" || got["tool_use_id"] != "c1" {
		t.Fatalf("stdin = %s", data)
	}
	if got["tool_input"].(map[string]any)["command"] != "ls" {
		t.Fatalf("tool_input = %v", got["tool_input"])
	}
}

func TestMatcherFiltersToolEvents(t *testing.T) {
	dir := t.TempDir()
	mark := filepath.Join(dir, "ran")
	e := engine(PreToolUse, "edit", "touch "+mark)
	if !e.Has(PreToolUse) {
		t.Fatal("has")
	}
	e.Run(context.Background(), Input{Event: PreToolUse, Cwd: dir, ToolName: "shell"})
	if _, err := os.Stat(mark); err == nil {
		t.Fatal("hook ran for a non-matching tool")
	}
	e.Run(context.Background(), Input{Event: PreToolUse, Cwd: dir, ToolName: "edit"})
	if _, err := os.Stat(mark); err != nil {
		t.Fatal("hook did not run for a matching tool")
	}
}

func TestExitTwoBlocksWithStderr(t *testing.T) {
	e := engine(PreToolUse, "", script(t, `echo "no rm please" >&2; exit 2`))
	out := e.Run(context.Background(), Input{Event: PreToolUse, Cwd: t.TempDir(), ToolName: "shell"})
	if !out.Blocked || out.Reason != "no rm please" {
		t.Fatalf("out = %+v", out)
	}
	if len(out.Notes) != 1 || out.Notes[0].Outcome != NoteBlocked {
		t.Fatalf("notes = %+v", out.Notes)
	}
}

func TestExitTwoWithoutReasonIsAnError(t *testing.T) {
	e := engine(PreToolUse, "", "exit 2")
	out := e.Run(context.Background(), Input{Event: PreToolUse, Cwd: t.TempDir(), ToolName: "shell"})
	if out.Blocked || len(out.Notes) != 1 || out.Notes[0].Outcome != NoteError {
		t.Fatalf("out = %+v", out)
	}
}

func TestOtherExitIsNonBlockingError(t *testing.T) {
	e := engine(Stop, "", script(t, `echo boom >&2; exit 1`))
	out := e.Run(context.Background(), Input{Event: Stop, Cwd: t.TempDir()})
	if out.Blocked || len(out.Notes) != 1 || out.Notes[0].Outcome != NoteError || !strings.Contains(out.Notes[0].Message, "boom") {
		t.Fatalf("out = %+v", out)
	}
}

func TestJSONOutputDenyAndContext(t *testing.T) {
	e := engine(PreToolUse, "", script(t, `cat <<'EOF'
{"systemMessage":"heads up","hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"not on main","additionalContext":"branch is main"}}
EOF`))
	out := e.Run(context.Background(), Input{Event: PreToolUse, Cwd: t.TempDir(), ToolName: "shell"})
	if !out.Blocked || out.Reason != "not on main" || out.Decision != DecisionDeny {
		t.Fatalf("out = %+v", out)
	}
	if len(out.Context) != 1 || out.Context[0] != "branch is main" {
		t.Fatalf("context = %v", out.Context)
	}
	if len(out.Messages) != 1 || out.Messages[0] != "heads up" {
		t.Fatalf("messages = %v", out.Messages)
	}
}

func TestJSONAllowWithUpdatedInput(t *testing.T) {
	e := engine(PreToolUse, "", `echo '{"hookSpecificOutput":{"permissionDecision":"allow","updatedInput":{"command":"ls -la"}}}'`)
	out := e.Run(context.Background(), Input{Event: PreToolUse, Cwd: t.TempDir(), ToolName: "shell"})
	if out.Blocked || out.Decision != DecisionAllow || string(out.UpdatedInput) != `{"command":"ls -la"}` {
		t.Fatalf("out = %+v", out)
	}
}

func TestDenyBeatsAskBeatsAllow(t *testing.T) {
	e := engine(PreToolUse, "",
		`echo '{"hookSpecificOutput":{"permissionDecision":"allow"}}'`,
		`echo '{"hookSpecificOutput":{"permissionDecision":"ask","permissionDecisionReason":"double check"}}'`,
	)
	out := e.Run(context.Background(), Input{Event: PreToolUse, Cwd: t.TempDir(), ToolName: "shell"})
	if out.Decision != DecisionAsk || out.Blocked {
		t.Fatalf("out = %+v", out)
	}
}

func TestLegacyDecisionBlock(t *testing.T) {
	e := engine(Stop, "", `echo '{"decision":"block","reason":"tests are failing"}'`)
	out := e.Run(context.Background(), Input{Event: Stop, Cwd: t.TempDir()})
	if !out.Blocked || out.Reason != "tests are failing" {
		t.Fatalf("out = %+v", out)
	}
}

func TestContinueFalseStops(t *testing.T) {
	e := engine(PostToolUse, "", `echo '{"continue":false,"stopReason":"enough"}'`)
	out := e.Run(context.Background(), Input{Event: PostToolUse, Cwd: t.TempDir(), ToolName: "shell"})
	if !out.Stop || out.StopReason != "enough" {
		t.Fatalf("out = %+v", out)
	}
}

func TestPlainStdoutIsContextForPromptAndStart(t *testing.T) {
	e := engine(UserPromptSubmit, "", `echo "today is friday"`)
	out := e.Run(context.Background(), Input{Event: UserPromptSubmit, Cwd: t.TempDir(), Prompt: "hi"})
	if len(out.Context) != 1 || out.Context[0] != "today is friday" {
		t.Fatalf("out = %+v", out)
	}
	e = engine(PostToolUse, "", `echo "just logging"`)
	out = e.Run(context.Background(), Input{Event: PostToolUse, Cwd: t.TempDir(), ToolName: "shell"})
	if len(out.Context) != 0 || len(out.Notes) != 0 {
		t.Fatalf("plain stdout on PostToolUse should be ignored, got %+v", out)
	}
}

func TestInvalidJSONIsAnError(t *testing.T) {
	e := engine(Stop, "", `echo '{"decision":'`)
	out := e.Run(context.Background(), Input{Event: Stop, Cwd: t.TempDir()})
	if out.Blocked || len(out.Notes) != 1 || out.Notes[0].Outcome != NoteError {
		t.Fatalf("out = %+v", out)
	}
}

func TestTimeoutKillsTheProcessGroup(t *testing.T) {
	dir := t.TempDir()
	mark := filepath.Join(dir, "late")
	// The child sleeps in the background; killing only sh would leave it.
	e := &Engine{groups: map[Event][]activeGroup{Stop: {{matcher: matchAll, handlers: []Handler{{Command: "(sleep 2; touch " + mark + ") & wait", Timeout: 1}}}}}}
	start := time.Now()
	out := e.Run(context.Background(), Input{Event: Stop, Cwd: dir})
	if time.Since(start) > 1800*time.Millisecond {
		t.Fatalf("timeout not enforced: %v", time.Since(start))
	}
	if len(out.Notes) != 1 || out.Notes[0].Outcome != NoteError || !strings.Contains(out.Notes[0].Message, "timed out") {
		t.Fatalf("out = %+v", out)
	}
	time.Sleep(1500 * time.Millisecond)
	if _, err := os.Stat(mark); err == nil {
		t.Fatal("background child survived the timeout")
	}
}

func TestCancelledContextStopsHooks(t *testing.T) {
	e := engine(Stop, "", "sleep 5")
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(100 * time.Millisecond); cancel() }()
	start := time.Now()
	e.Run(ctx, Input{Event: Stop, Cwd: t.TempDir()})
	if time.Since(start) > 2*time.Second {
		t.Fatal("cancel not honoured")
	}
}

func TestHandlersRunInParallelAndDedupe(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "log")
	cmd := "sleep 0.5; echo x >> " + log
	e := engine(Stop, "", cmd, cmd, "sleep 0.5")
	start := time.Now()
	e.Run(context.Background(), Input{Event: Stop, Cwd: dir})
	if time.Since(start) > 1200*time.Millisecond {
		t.Fatalf("handlers ran serially: %v", time.Since(start))
	}
	data, _ := os.ReadFile(log)
	if strings.Count(string(data), "x") != 1 {
		t.Fatalf("duplicate command ran %d times", strings.Count(string(data), "x"))
	}
}

func TestEnvExposesProjectDir(t *testing.T) {
	dir := t.TempDir()
	e := engine(UserPromptSubmit, "", `echo "$PALADIN_PROJECT_DIR|$CLAUDE_PROJECT_DIR"`)
	out := e.Run(context.Background(), Input{Event: UserPromptSubmit, Cwd: dir})
	if len(out.Context) != 1 || out.Context[0] != dir+"|"+dir {
		t.Fatalf("out = %+v", out)
	}
}

func TestOutputIsCapped(t *testing.T) {
	e := engine(UserPromptSubmit, "", `head -c 200000 /dev/zero | tr '\0' 'a'`)
	out := e.Run(context.Background(), Input{Event: UserPromptSubmit, Cwd: t.TempDir()})
	if len(out.Context) != 1 || len(out.Context[0]) > maxOutput+64 {
		t.Fatalf("context = %d items, notes %+v", len(out.Context), out.Notes)
	}
}

func TestSessionStartMatchesSource(t *testing.T) {
	dir := t.TempDir()
	mark := filepath.Join(dir, "ran")
	e := engine(SessionStart, "startup", "touch "+mark)
	e.Run(context.Background(), Input{Event: SessionStart, Cwd: dir, Source: "resume"})
	if _, err := os.Stat(mark); err == nil {
		t.Fatal("startup hook ran on resume")
	}
	e.Run(context.Background(), Input{Event: SessionStart, Cwd: dir, Source: "startup"})
	if _, err := os.Stat(mark); err != nil {
		t.Fatal("startup hook did not run")
	}
}
