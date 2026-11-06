// plan_test.go covers the unfinished-plan nudge: an agent that
// answers while steps are still open is sent back to work, and a
// model that will not settle its list still ends after maxPlanNudges.

package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/inference"
	"github.com/Ady-JK/paladin/core/internal/tools"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

func planAgent(t *testing.T, prov *mockProvider) (*Agent, transcript.Session) {
	t.Helper()
	a, sess, _, done := testAgent(t, prov)
	t.Cleanup(func() { close(done) })
	a.tools.Register(tools.TodoTool{})
	a.toolDefs = append(a.toolDefs, inference.ToolDefinition{Name: "todo", Description: "t"})
	return a, sess
}

func writeTodos(t *testing.T, dir, raw string) {
	t.Helper()
	// The session file wraps the list: {"todos":[...]}.
	if err := os.WriteFile(filepath.Join(dir, "todos.json"), []byte(`{"todos":`+raw+`}`), 0o600); err != nil {
		t.Fatal(err)
	}
}

func todoArgs(raw string) string { return `{"todos":` + raw + `}` }

func countNudges(prov *mockProvider) (int, string) {
	if len(prov.streamMsgs) == 0 {
		return 0, ""
	}
	// Every fired nudge appends one user message, so the final round's
	// snapshot holds them all; earlier snapshots repeat the same rows.
	n := 0
	last := ""
	for _, m := range prov.streamMsgs[len(prov.streamMsgs)-1] {
		if m.Role == "user" && strings.Contains(m.Content, "activity list still has unfinished steps") {
			n++
			last = m.Content
		}
	}
	return n, last
}

func TestPlanNudgeSendsAgentBackToWork(t *testing.T) {
	prov := &mockProvider{}
	a, sess := planAgent(t, prov)
	writeTodos(t, sess.Dir, `[{"content":"Add the flag","status":"in_progress"},{"content":"Write docs","status":"not_started"}]`)
	prov.streams = []streamScript{
		callRound("t1", "todo", todoArgs(`[{"content":"Add the flag","status":"finished"},{"content":"Write docs","status":"in_progress"}]`)),
		textRound("half done"),
		callRound("t2", "todo", todoArgs(`[{"content":"Add the flag","status":"finished"},{"content":"Write docs","status":"finished"}]`)),
		textRound("done"),
	}
	out, err := a.Run()
	if err != nil {
		t.Fatal(err)
	}
	if prov.calls != 4 {
		t.Fatalf("want 4 rounds, got %d", prov.calls)
	}
	n, last := countNudges(prov)
	if n != 1 {
		t.Fatalf("want exactly one plan nudge, got %d", n)
	}
	if !strings.Contains(last, "Write docs") || !strings.Contains(last, "in_progress") {
		t.Fatalf("the nudge names the open step: %q", last)
	}
	if !strings.Contains(out.Reply, "done") {
		t.Fatalf("reply %q", out.Reply)
	}
	h, _ := sess.History()
	found := false
	for _, m := range h {
		if m.Role == transcript.RoleUser && m.Display == planDisplay {
			found = true
		}
	}
	if !found {
		t.Fatalf("the nudge is kept in history with a short display: %+v", h)
	}
}

func TestPlanNudgeCapsStubbornModels(t *testing.T) {
	prov := &mockProvider{}
	a, sess := planAgent(t, prov)
	writeTodos(t, sess.Dir, `[{"content":"Never settling","status":"in_progress"}]`)
	prov.streams = []streamScript{
		textRound("t1"), textRound("t2"), textRound("t3"),
		textRound("t4"), textRound("t5"), textRound("t6"),
	}
	out, err := a.Run()
	if err != nil {
		t.Fatal(err)
	}
	// One initial round plus maxPlanNudges extra rounds; the rest of
	// the script must never run.
	if prov.calls != 1+maxPlanNudges {
		t.Fatalf("want %d rounds, got %d", 1+maxPlanNudges, prov.calls)
	}
	if n, _ := countNudges(prov); n != maxPlanNudges {
		t.Fatalf("want %d nudges, got %d", maxPlanNudges, n)
	}
	if !strings.Contains(out.Reply, "t4") {
		t.Fatalf("the turn ends on the last allowed round: %q", out.Reply)
	}
}

func TestPlanNudgeStaysQuietWithoutOpenSteps(t *testing.T) {
	cases := map[string]string{
		"no list file": "",
		"all finished": `[{"content":"A","status":"finished"},{"content":"B","status":"cancelled"}]`,
		"empty list":   `[]`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			prov := &mockProvider{streams: []streamScript{textRound("done")}}
			a, sess := planAgent(t, prov)
			if raw != "" {
				writeTodos(t, sess.Dir, raw)
			}
			out, err := a.Run()
			if err != nil {
				t.Fatal(err)
			}
			if prov.calls != 1 || !strings.Contains(out.Reply, "done") {
				t.Fatalf("calls=%d reply=%q", prov.calls, out.Reply)
			}
			if n, _ := countNudges(prov); n != 0 {
				t.Fatalf("no nudge expected, got %d", n)
			}
		})
	}
}

// TestPlanNudgeResetsAfterProgress pins the fix for a real stall: a
// turn that does work between stalls must keep its reminders, instead
// of dying after three of them halfway through a long plan.
func TestPlanNudgeResetsAfterProgress(t *testing.T) {
	prov := &mockProvider{}
	a, sess := planAgent(t, prov)
	writeTodos(t, sess.Dir, `[{"content":"Ship it","status":"in_progress"}]`)
	stalls := maxPlanNudges + 1 // one past the cap: only a reset keeps going
	var scripts []streamScript
	for i := 0; i < stalls; i++ {
		scripts = append(scripts, textRound("still working"))
		if i < stalls-1 {
			// Real work between stalls: the reminder budget restarts.
			scripts = append(scripts, callRound("r", "read", `{"path":"/nonexistent"}`))
		}
	}
	scripts = append(scripts,
		callRound("done", "todo", todoArgs(`[{"content":"Ship it","status":"finished"}]`)),
		textRound("done"),
	)
	prov.streams = scripts
	out, err := a.Run()
	if err != nil {
		t.Fatalf("a turn making progress must not fail: %v", err)
	}
	if !strings.Contains(out.Reply, "done") {
		t.Fatalf("reply %q", out.Reply)
	}
	if n, _ := countNudges(prov); n != stalls {
		t.Fatalf("want %d nudges across the stalls, got %d", stalls, n)
	}
}

// TestPlanStalledFailsLoudly pins the honest ending: a turn that
// changed files, ignored every reminder and left steps open is a
// failure the user can resume, not a calm completion.
func TestPlanStalledFailsLoudly(t *testing.T) {
	prov := &mockProvider{}
	a, sess := planAgent(t, prov)
	writeTodos(t, sess.Dir, `[{"content":"First step","status":"in_progress"},
		{"content":"Second step","status":"not_started"}]`)
	// The turn changed a file: it is executing a plan, not answering a
	// question. (The write tool itself is not under test here.)
	a.changed = 1
	for i := 0; i < maxPlanNudges+1; i++ {
		prov.streams = append(prov.streams, textRound("next I will write the code"))
	}
	_, err := a.Run()
	if err == nil {
		t.Fatal("a stalled plan must fail, not end as a normal completion")
	}
	msg := err.Error()
	for _, want := range []string{"still open", "First step", "resume"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q lacks %q", msg, want)
		}
	}
	// The streamed text is still in history: the user sees what it said.
	h, _ := sess.History()
	found := false
	for _, m := range h {
		if m.Role == transcript.RoleAssistant && strings.Contains(m.Content, "next I will write") {
			found = true
		}
	}
	if !found {
		t.Fatal("the stalled turn's text must be kept")
	}
}

// TestPlanStaleListDoesNotFailTurn pins the guard against false
// failures: a turn that changed nothing (a question, a review) is not a
// stalled plan, even when an older turn left steps open — it ends
// normally however often it is reminded.
func TestPlanStaleListDoesNotFailTurn(t *testing.T) {
	prov := &mockProvider{}
	a, sess := planAgent(t, prov)
	writeTodos(t, sess.Dir, `[{"content":"Old work","status":"in_progress"}]`)
	prov.streams = []streamScript{
		textRound("here is the answer"),
		textRound("still the answer"),
		textRound("still the answer"),
		textRound("still the answer"),
	}
	out, err := a.Run()
	if err != nil {
		t.Fatalf("a stale list must not fail the turn: %v", err)
	}
	if !strings.Contains(out.Reply, "answer") {
		t.Fatalf("reply %q", out.Reply)
	}
}

// TestNoCheckNudgeAfterEditing pins the removal of the old
// check-before-finishing reminder: a turn that only edits code and stops
// now ends on the model's own answer, with no injected user message.
func TestNoCheckNudgeAfterEditing(t *testing.T) {
	prov := &mockProvider{}
	a, ws := planAgent(t, prov)
	// Register the real write tool: a call to an unregistered tool errors
	// out, which would take the old reminder down with it and make this
	// test pass for the wrong reason.
	a.tools.Register(&tools.WriteTool{})
	a.toolDefs = append(a.toolDefs, inference.ToolDefinition{Name: "write", Description: "w"})
	prov.streams = []streamScript{
		callRound("w", "write", `{"path":"`+filepath.Join(ws.Dir, "x.go")+`","content":"package x"}`),
		textRound("done"),
	}
	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	if prov.calls != 2 {
		t.Fatalf("a plain edit should end the turn: %d calls", prov.calls)
	}
	for _, m := range prov.streamMsgs[1] {
		if m.Role == "user" && strings.Contains(m.Content, "Before you finish") {
			t.Fatalf("a check reminder was injected: %q", m.Content)
		}
	}
}
