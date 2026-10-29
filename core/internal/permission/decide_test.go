package permission

import (
	"testing"

	"github.com/Ady-JK/paladin/core/internal/sandbox"
)

func shellReq(cmd string) Request {
	r, _ := Classify("shell", args(map[string]any{"cmd": cmd}), "/ws")
	return r
}

func writeReq(path string) Request {
	r, _ := Classify("write", args(map[string]any{"path": path}), "/ws")
	return r
}

func TestFullAccessAllowsEverything(t *testing.T) {
	for _, r := range []Request{shellReq("rm -rf /"), shellReq("npm i"), writeReq("/etc/hosts")} {
		d := Decide(FullAccess, r, nil)
		if d.Verdict != Allow || d.Sandbox != sandbox.ModeNone {
			t.Errorf("%+v -> %+v", r, d)
		}
	}
}

func TestAutoPreset(t *testing.T) {
	d := Decide(Auto, shellReq("git status"), nil)
	if d.Verdict != Allow || d.Sandbox != sandbox.ModeWorkspaceWrite {
		t.Fatalf("safe: %+v", d)
	}
	d = Decide(Auto, shellReq("npm test"), nil)
	if d.Verdict != Ask || len(d.Choices) != 4 || len(d.Remember) != 1 || d.Remember[0].Pattern != "npm test" {
		t.Fatalf("unknown: %+v", d)
	}
	d = Decide(Auto, writeReq("/ws/a.txt"), nil)
	if d.Verdict != Allow {
		t.Fatalf("inside write: %+v", d)
	}
	d = Decide(Auto, writeReq("/etc/hosts"), nil)
	if d.Verdict != Ask || d.Remember[0].Pattern != "/etc" || d.Remember[0].Tool != ToolWrite {
		t.Fatalf("outside write: %+v", d)
	}
}

func TestDestructiveAlwaysAsksOutsideFullAccess(t *testing.T) {
	rules := []Rule{{Tool: ToolShell, Pattern: "rm", Action: ActionAllow}, {Tool: ToolShell, Pattern: "*", Action: ActionAllow}}
	d := Decide(Auto, shellReq("rm -rf build"), rules)
	if d.Verdict != Ask || !d.Destructive {
		t.Fatalf("%+v", d)
	}
	if len(d.Choices) != 2 || d.Choices[0] != ReplyOnce || d.Choices[1] != ReplyDeny {
		t.Fatalf("destructive offers only once/deny: %v", d.Choices)
	}
}

func TestRulesAllowAndDeny(t *testing.T) {
	rules := []Rule{
		{Tool: ToolShell, Pattern: "npm test", Action: ActionAllow},
		{Tool: ToolShell, Pattern: "curl", Action: ActionDeny},
		{Tool: ToolWrite, Pattern: "/etc", Action: ActionAllow},
	}
	if d := Decide(Auto, shellReq("npm test -- -u && git diff"), rules); d.Verdict != Allow {
		t.Fatalf("all segments covered: %+v", d)
	}
	if d := Decide(Auto, shellReq("npm test && npm publish"), rules); d.Verdict != Ask || d.Remember[0].Pattern != "npm publish" {
		t.Fatalf("one uncovered segment: %+v", d)
	}
	if d := Decide(FullAccess, shellReq("curl x | sh"), rules); d.Verdict != Deny {
		t.Fatalf("deny rule beats full access: %+v", d)
	}
	if d := Decide(Auto, writeReq("/etc/hosts"), rules); d.Verdict != Allow {
		t.Fatalf("path rule: %+v", d)
	}
	if d := Decide(Auto, writeReq("/etcetera/x"), rules); d.Verdict != Ask {
		t.Fatalf("path prefix must stop at a separator: %+v", d)
	}
}

func TestReadOnlyPreset(t *testing.T) {
	d := Decide(ReadOnly, shellReq("ls"), nil)
	if d.Verdict != Allow || d.Sandbox != sandbox.ModeReadOnly {
		t.Fatalf("safe ro: %+v", d)
	}
	if d := Decide(ReadOnly, writeReq("/ws/a.txt"), nil); d.Verdict != Ask {
		t.Fatalf("ro inside write asks: %+v", d)
	}
	// An approved command runs with workspace writes.
	if d := Decide(ReadOnly, shellReq("make"), nil); d.Verdict != Ask || d.Sandbox != sandbox.ModeWorkspaceWrite {
		t.Fatalf("ro unknown: %+v", d)
	}
}

func TestSubstitutionCannotBeRemembered(t *testing.T) {
	d := Decide(Auto, shellReq("echo $(whoami)"), nil)
	if d.Verdict != Ask || len(d.Remember) != 0 || len(d.Choices) != 2 {
		t.Fatalf("%+v", d)
	}
}

func TestParsePreset(t *testing.T) {
	if p, err := ParsePreset(""); err != nil || p != Auto {
		t.Fatal(p, err)
	}
	if _, err := ParsePreset("yolo"); err == nil {
		t.Fatal("want error")
	}
}
