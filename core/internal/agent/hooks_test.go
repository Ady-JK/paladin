//go:build unix

package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/hooks"
	"github.com/Ady-JK/paladin/core/internal/inference"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

// withHooks installs a global hooks.json and gives the agent its engine.
func withHooks(t *testing.T, a *Agent, config string) {
	t.Helper()
	m := hooks.NewManager(t.TempDir())
	if err := m.SetConfig(hooks.ScopeGlobal, "", config); err != nil {
		t.Fatal(err)
	}
	a.hooks = m.Engine(a.toolCtx.Workspace)
}

// hookScript writes an executable script and returns a JSON-quoted
// command for it.
func hookScript(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "hook.sh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return `"` + p + `"`
}

func hookChunks(chunks []ChatChunk) []hooks.Note {
	var out []hooks.Note
	for _, c := range chunks {
		if c.Hook != nil {
			out = append(out, *c.Hook)
		}
	}
	return out
}

func TestPreToolUseExitTwoBlocksTheCall(t *testing.T) {
	prov := &mockProvider{streams: []streamScript{callRound("c1", "read", `{"path":"x.txt"}`), textRound("ok")}}
	a, _, tx, done := testAgent(t, prov)
	defer close(done)
	withHooks(t, a, `{"hooks":{"PreToolUse":[{"matcher":"Read","hooks":[{"command":`+hookScript(t, `echo "reads are off today" >&2; exit 2`)+`}]}]}}`)
	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	got := toolResultsByID(t, a)["c1"]
	if !strings.Contains(got, "reads are off today") || !strings.Contains(got, "PreToolUse hook") {
		t.Fatalf("result %q", got)
	}
	notes := hookChunks(drainChunks(tx))
	if len(notes) != 1 || notes[0].Outcome != hooks.NoteBlocked || notes[0].Event != hooks.PreToolUse {
		t.Fatalf("notes %+v", notes)
	}
}

func TestPreToolUseUpdatedInputRewritesTheCall(t *testing.T) {
	prov := &mockProvider{streams: []streamScript{callRound("c1", "read", `{"path":"wrong.txt"}`), textRound("ok")}}
	a, sess, _, done := testAgent(t, prov)
	defer close(done)
	if err := os.WriteFile(filepath.Join(sess.Meta.Cwd, "right.txt"), []byte("the-right-file"), 0o644); err != nil {
		t.Fatal(err)
	}
	right := filepath.Join(sess.Meta.Cwd, "right.txt")
	withHooks(t, a, `{"hooks":{"PreToolUse":[{"hooks":[{"command":"echo '{\"hookSpecificOutput\":{\"permissionDecision\":\"allow\",\"updatedInput\":{\"path\":\"`+right+`\"}}}'"}]}]}}`)
	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	if got := toolResultsByID(t, a)["c1"]; !strings.Contains(got, "the-right-file") {
		t.Fatalf("result %q", got)
	}
}

func TestPostToolUseContextJoinsTheToolResult(t *testing.T) {
	prov := &mockProvider{streams: []streamScript{callRound("c1", "read", `{"path":"x.txt"}`), textRound("ok")}}
	a, _, _, done := testAgent(t, prov)
	defer close(done)
	withHooks(t, a, `{"hooks":{"PostToolUse":[{"matcher":"read","hooks":[{"command":"echo '{\"hookSpecificOutput\":{\"additionalContext\":\"remember the lint rules\"}}'"}]}]}}`)
	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	if got := toolResultsByID(t, a)["c1"]; !strings.Contains(got, "remember the lint rules") {
		t.Fatalf("result %q", got)
	}
}

func TestPostToolUseReceivesTheResult(t *testing.T) {
	sess := testSession(t, "00000000-0000-4000-8000-000000000001")
	x := filepath.Join(sess.Meta.Cwd, "x.txt")
	prov := &mockProvider{streams: []streamScript{callRound("c1", "read", `{"path":"`+x+`"}`), textRound("ok")}}
	a, _, done := newAgentForTest(t, prov, sess, "hi", false, true)
	defer close(done)
	if err := os.WriteFile(x, []byte("payload-123"), 0o644); err != nil {
		t.Fatal(err)
	}
	dump := filepath.Join(t.TempDir(), "in.json")
	withHooks(t, a, `{"hooks":{"PostToolUse":[{"hooks":[{"command":"cat > `+dump+`"}]}]}}`)
	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(dump)
	if !strings.Contains(string(data), "payload-123") || !strings.Contains(string(data), `"tool_use_id":"c1"`) {
		t.Fatalf("stdin %s", data)
	}
}

func TestUserPromptSubmitAddsContextButKeepsDisplay(t *testing.T) {
	prov := &mockProvider{streams: []streamScript{textRound("ok")}}
	a, sess, _, done := testAgent(t, prov)
	defer close(done)
	withHooks(t, a, `{"hooks":{"UserPromptSubmit":[{"hooks":[{"command":"echo the sprint ends friday"}]}]}}`)
	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	if got := lastUser(prov.streamMsgs[0]); !strings.HasPrefix(got, "hi") || !strings.Contains(got, "the sprint ends friday") {
		t.Fatalf("model saw %q", got)
	}
	h, _ := sess.History()
	if h[0].Role != transcript.RoleUser || h[0].Display != "hi" || !strings.Contains(h[0].Content, "sprint") {
		t.Fatalf("persisted %+v", h[0])
	}
}

func TestUserPromptSubmitBlockRejectsThePrompt(t *testing.T) {
	prov := &mockProvider{streams: []streamScript{textRound("ok")}}
	a, sess, _, done := testAgent(t, prov)
	defer close(done)
	withHooks(t, a, `{"hooks":{"UserPromptSubmit":[{"hooks":[{"command":`+hookScript(t, `echo "no secrets in prompts" >&2; exit 2`)+`}]}]}}`)
	_, err := a.Run()
	if err == nil || !strings.Contains(err.Error(), "no secrets in prompts") {
		t.Fatalf("err %v", err)
	}
	if prov.calls != 0 {
		t.Fatal("a blocked prompt must not reach the model")
	}
	if h, _ := sess.History(); len(h) != 0 {
		t.Fatalf("a blocked prompt must not be saved: %+v", h)
	}
}

func TestSessionStartContextOnFirstTurn(t *testing.T) {
	prov := &mockProvider{streams: []streamScript{textRound("ok")}}
	a, _, _, done := testAgent(t, prov)
	defer close(done)
	a.sessionStart = "startup"
	withHooks(t, a, `{"hooks":{"SessionStart":[{"matcher":"startup","hooks":[{"command":"echo house style: tabs"}]}]}}`)
	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	if got := lastUser(prov.streamMsgs[0]); !strings.Contains(got, "house style: tabs") {
		t.Fatalf("model saw %q", got)
	}
}

const stopOnce = `input=$(cat)
case "$input" in *'"stop_hook_active":true'*) exit 0;; esac
echo "run the tests first" >&2
exit 2`

func TestStopHookBlockContinuesTheTurn(t *testing.T) {
	prov := &mockProvider{streams: []streamScript{textRound("first"), textRound("second")}}
	a, sess, _, done := testAgent(t, prov)
	defer close(done)
	withHooks(t, a, `{"hooks":{"Stop":[{"hooks":[{"command":`+hookScript(t, stopOnce)+`}]}]}}`)
	out, err := a.Run()
	if err != nil {
		t.Fatal(err)
	}
	if prov.calls != 2 || !strings.Contains(lastUser(prov.streamMsgs[1]), "run the tests first") {
		t.Fatalf("calls=%d", prov.calls)
	}
	second := prov.streamMsgs[1]
	sawFirst := false
	for _, m := range second {
		if m.Role == "assistant" && m.Content == "first" {
			sawFirst = true
		}
	}
	if !sawFirst {
		t.Fatalf("the model must see its own earlier answer: %+v", second)
	}
	if !strings.Contains(out.Reply, "second") {
		t.Fatalf("reply %q", out.Reply)
	}
	h, _ := sess.History()
	var users int
	for _, m := range h {
		if m.Role == transcript.RoleUser {
			users++
		}
	}
	if users != 2 {
		t.Fatalf("hook feedback should persist as a user message: %+v", h)
	}
}

func TestStopHookContinuationsAreCapped(t *testing.T) {
	var streams []streamScript
	for i := 0; i < maxStopContinuations+3; i++ {
		streams = append(streams, textRound("again"))
	}
	prov := &mockProvider{streams: streams}
	a, _, _, done := testAgent(t, prov)
	defer close(done)
	withHooks(t, a, `{"hooks":{"Stop":[{"hooks":[{"command":`+hookScript(t, `cat >/dev/null; echo "keep going" >&2; exit 2`)+`}]}]}}`)
	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	if prov.calls != maxStopContinuations+1 {
		t.Fatalf("calls=%d", prov.calls)
	}
}

func TestContinueFalseEndsTheTurnAfterTools(t *testing.T) {
	prov := &mockProvider{streams: []streamScript{callRound("c1", "read", `{"path":"x.txt"}`), textRound("never")}}
	a, _, tx, done := testAgent(t, prov)
	defer close(done)
	withHooks(t, a, `{"hooks":{"PostToolUse":[{"hooks":[{"command":"echo '{\"continue\":false,\"stopReason\":\"budget review\"}'"}]}]}}`)
	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	if prov.calls != 1 {
		t.Fatalf("calls=%d", prov.calls)
	}
	notes := hookChunks(drainChunks(tx))
	if len(notes) != 1 || notes[0].Outcome != hooks.NoteStopped {
		t.Fatalf("notes %+v", notes)
	}
}

func TestPreCompactBlockSkipsCompaction(t *testing.T) {
	prov := &mockProvider{streams: []streamScript{textRound("ok")}, complete: inference.ToolResponse{Content: "SUM"}}
	a, sess, _, done := testAgent(t, prov)
	defer close(done)
	a.window = 400
	seedHistory(t, a, sess, 3, 400)
	withHooks(t, a, `{"hooks":{"PreCompact":[{"matcher":"auto","hooks":[{"command":`+hookScript(t, `echo "not now" >&2; exit 2`)+`}]}]}}`)
	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	if len(prov.completeMsgs) != 0 {
		t.Fatal("PreCompact block must skip the summary call")
	}
}

func TestCompactHooksAddInstructionsAndRun(t *testing.T) {
	prov := &mockProvider{streams: []streamScript{textRound("ok")}, complete: inference.ToolResponse{Content: "SUM"}}
	a, sess, _, done := testAgent(t, prov)
	defer close(done)
	a.window = 400
	seedHistory(t, a, sess, 3, 400)
	mark := filepath.Join(t.TempDir(), "post")
	withHooks(t, a, `{"hooks":{
	  "PreCompact":[{"hooks":[{"command":"echo '{\"hookSpecificOutput\":{\"additionalContext\":\"keep the API names\"}}'"}]}],
	  "PostCompact":[{"hooks":[{"command":"touch `+mark+`"}]}]}}`)
	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	if len(prov.completeMsgs) != 1 || !strings.Contains(prov.completeMsgs[0][0].Content, "keep the API names") {
		t.Fatalf("summary prompt %+v", prov.completeMsgs)
	}
	if _, err := os.Stat(mark); err != nil {
		t.Fatal("PostCompact did not run")
	}
}

func TestNoHooksMeansNoHookChunks(t *testing.T) {
	prov := &mockProvider{streams: []streamScript{callRound("c1", "read", `{"path":"x.txt"}`), textRound("ok")}}
	a, _, tx, done := testAgent(t, prov)
	defer close(done)
	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	if notes := hookChunks(drainChunks(tx)); len(notes) != 0 {
		t.Fatalf("notes %+v", notes)
	}
}
