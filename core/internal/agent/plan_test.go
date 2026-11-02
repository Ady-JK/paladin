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
