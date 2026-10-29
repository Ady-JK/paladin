package agent

import (
	"errors"
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/inference"
	"github.com/Ady-JK/paladin/core/internal/tools"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

type fakeContext struct{ sections []string }

func (f fakeContext) SystemSections(transcript.Session) []string { return f.sections }

type fakeCommands map[string]Command

func (f fakeCommands) LoadCommand(cwd, name, args string) (Command, error) {
	c, ok := f[name]
	if !ok {
		return Command{}, errors.New("not found: command " + name)
	}
	c.Prompt = strings.ReplaceAll(c.Prompt, "$ARGUMENTS", args)
	return c, nil
}

func TestBuildAppendsSystemSections(t *testing.T) {
	sess := testSession(t, "00000000-0000-4000-8000-000000000031")
	d, _, _, _ := testDeps(t, sess)
	d.Sections = fakeContext{sections: []string{"AGENTS SECTION", "", "MEMORY SECTION"}}
	a, err := DefaultTemplate().Build(d, Request{SessionID: sess.Meta.SessionID, Message: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(a.system, "runtime base") || !strings.HasSuffix(a.system, "AGENTS SECTION\n\nMEMORY SECTION") {
		t.Fatalf("system = %q", a.system)
	}
}

func TestBuildExpandsCommandWithDisplayAndPlanMode(t *testing.T) {
	sess := testSession(t, "00000000-0000-4000-8000-000000000032")
	d, _, _, _ := testDeps(t, sess)
	d.Commands = fakeCommands{"review": {Name: "review", Prompt: "Review $ARGUMENTS carefully", Mode: "plan"}}
	cmd := "review"
	a, err := DefaultTemplate().Build(d, Request{SessionID: sess.Meta.SessionID, Message: "src/", Command: &cmd})
	if err != nil {
		t.Fatal(err)
	}
	if a.currentMessage != "Review src/ carefully" || a.currentDisplay != "/review src/" {
		t.Fatalf("message=%q display=%q", a.currentMessage, a.currentDisplay)
	}
	if _, ok := a.tools.Get("write"); ok {
		t.Fatal("plan mode kept write")
	}
	if _, ok := a.tools.Get("edit"); ok {
		t.Fatal("plan mode kept edit")
	}
	sh, _ := a.tools.Get("shell")
	if _, ok := sh.(*tools.PlanShell); !ok {
		t.Fatalf("shell = %T", sh)
	}
	if !strings.Contains(a.system, "PLAN MODE") {
		t.Fatal("plan addendum missing")
	}
	// A command needs no typed text; an unknown one fails.
	if _, err := DefaultTemplate().Build(d, Request{SessionID: sess.Meta.SessionID, Command: &cmd}); err != nil {
		t.Fatalf("empty-arg command rejected: %v", err)
	}
	bad := "nope"
	if _, err := DefaultTemplate().Build(d, Request{SessionID: sess.Meta.SessionID, Command: &bad}); err == nil {
		t.Fatal("unknown command accepted")
	}
	d.Commands = nil
	if _, err := DefaultTemplate().Build(d, Request{SessionID: sess.Meta.SessionID, Command: &cmd}); err == nil {
		t.Fatal("command without source accepted")
	}
}

func TestBuildModePlanAndBuild(t *testing.T) {
	sess := testSession(t, "00000000-0000-4000-8000-000000000033")
	d, _, _, _ := testDeps(t, sess)
	a, err := DefaultTemplate().Build(d, Request{SessionID: sess.Meta.SessionID, Message: "plan it", Mode: "plan"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := a.tools.Get("write"); ok {
		t.Fatal("plan mode kept write")
	}
	if a.shell == nil {
		t.Fatal("plan mode lost the shell for job notices")
	}
	a, _ = DefaultTemplate().Build(d, Request{SessionID: sess.Meta.SessionID, Message: "go", Mode: "build"})
	if _, ok := a.tools.Get("write"); !ok {
		t.Fatal("build mode lost write")
	}
	if _, err := DefaultTemplate().Build(d, Request{SessionID: sess.Meta.SessionID, Message: "x", Mode: "yolo"}); err == nil {
		t.Fatal("unknown mode accepted")
	}
}

func TestBuildSkillSetsDisplay(t *testing.T) {
	sess := testSession(t, "00000000-0000-4000-8000-000000000034")
	d, _, _, _ := testDeps(t, sess)
	d.Skills = &fakeSkills{skills: map[string]Skill{"deploy": {Name: "deploy", Content: "BODY"}}}
	name := "deploy"
	a, err := DefaultTemplate().Build(d, Request{SessionID: sess.Meta.SessionID, Message: "ship", Skill: &name})
	if err != nil {
		t.Fatal(err)
	}
	if a.currentDisplay != "ship" {
		t.Fatalf("display = %q", a.currentDisplay)
	}
	if err := a.persist(a.currentMessage, false); err != nil {
		t.Fatal(err)
	}
	msgs, _ := sess.History()
	if msgs[0].Display != "ship" || !strings.Contains(msgs[0].Content, "BODY") {
		t.Fatalf("stored = %+v", msgs[0])
	}
}

func TestCompactOnlyTurn(t *testing.T) {
	provider := &mockProvider{complete: inference.ToolResponse{Content: "SUMMARY"}}
	sess := testSession(t, "00000000-0000-4000-8000-000000000035")
	for i := 0; i < 6; i++ {
		_ = sess.Append(transcript.RoleUser, strings.Repeat("question ", 50))
		_ = sess.Append(transcript.RoleAssistant, strings.Repeat("answer ", 50))
	}
	d, _, providers, _ := testDeps(t, sess)
	providers.provider = provider
	a, err := DefaultTemplate().Build(d, Request{SessionID: sess.Meta.SessionID, Compact: true})
	if err != nil {
		t.Fatal(err)
	}
	out, err := a.Run()
	if err != nil {
		t.Fatal(err)
	}
	if provider.calls != 0 {
		t.Fatal("compact-only turn streamed a model round")
	}
	msgs, _ := sess.History()
	var marker, users int
	for _, m := range msgs {
		if m.IsCompaction() {
			marker++
			if m.Compaction.Reason != "manual" || m.Content != "SUMMARY" {
				t.Fatalf("marker = %+v", m)
			}
		}
		if m.Role == transcript.RoleUser && !m.Replay {
			users++
		}
	}
	if marker != 1 || users != 6 {
		t.Fatalf("marker=%d users=%d out=%+v", marker, users, out)
	}
	// Nothing to compact is a clean error, not a crash.
	empty := testSession(t, "00000000-0000-4000-8000-000000000036")
	d2, _, p2, _ := testDeps(t, empty)
	p2.provider = provider
	a, _ = DefaultTemplate().Build(d2, Request{SessionID: empty.Meta.SessionID, Compact: true})
	if _, err := a.Run(); err == nil || !strings.Contains(err.Error(), "nothing") {
		t.Fatalf("empty compact err = %v", err)
	}
}
