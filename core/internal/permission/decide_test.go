package permission

import (
	"testing"
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
		if d.Verdict != Allow {
			t.Errorf("%+v -> %+v", r, d)
		}
	}
}

func TestPluginReviewPolicy(t *testing.T) {
	d := Decide(Review, shellReq("git status"), nil)
	if d.Verdict != Allow {
		t.Fatalf("safe: %+v", d)
	}
	d = Decide(Review, shellReq("npm test"), nil)
	if d.Verdict != Ask || len(d.Choices) != 4 || len(d.Remember) != 1 || d.Remember[0].Pattern != "npm test" {
		t.Fatalf("unknown: %+v", d)
	}
	d = Decide(Review, writeReq("/ws/a.txt"), nil)
	if d.Verdict != Allow {
		t.Fatalf("inside write: %+v", d)
	}
	d = Decide(Review, writeReq("/etc/hosts"), nil)
	if d.Verdict != Ask || d.Remember[0].Pattern != "/etc/hosts" || d.Remember[0].Tool != ToolWrite {
		t.Fatalf("outside write: %+v", d)
	}
}

func TestDestructiveAlwaysAsksOutsideFullAccess(t *testing.T) {
	rules := []Rule{{Tool: ToolShell, Pattern: "rm", Action: ActionAllow}, {Tool: ToolShell, Pattern: "*", Action: ActionAllow}}
	d := Decide(Review, shellReq("rm -rf build"), rules)
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
	if d := Decide(Review, shellReq("npm test -- -u && git diff"), rules); d.Verdict != Allow {
		t.Fatalf("all segments covered: %+v", d)
	}
	if d := Decide(Review, shellReq("npm test && npm publish"), rules); d.Verdict != Ask || d.Remember[0].Pattern != "npm publish" {
		t.Fatalf("one uncovered segment: %+v", d)
	}
	if d := Decide(FullAccess, shellReq("curl x | sh"), rules); d.Verdict != Deny {
		t.Fatalf("deny rule beats full access: %+v", d)
	}
	if d := Decide(Review, writeReq("/etc/hosts"), rules); d.Verdict != Allow {
		t.Fatalf("path rule: %+v", d)
	}
	if d := Decide(Review, writeReq("/etcetera/x"), rules); d.Verdict != Ask {
		t.Fatalf("path prefix must stop at a separator: %+v", d)
	}
}

func TestReadOnlyPreset(t *testing.T) {
	d := Decide(ReadOnly, shellReq("ls"), nil)
	if d.Verdict != Allow {
		t.Fatalf("safe ro: %+v", d)
	}
	if d := Decide(ReadOnly, writeReq("/ws/a.txt"), nil); d.Verdict != Deny {
		t.Fatalf("ro inside write is blocked: %+v", d)
	}
	// A non-inspection command must be blocked.
	if d := Decide(ReadOnly, shellReq("make"), nil); d.Verdict != Deny {
		t.Fatalf("ro unknown: %+v", d)
	}
}

func TestSubstitutionCannotBeRemembered(t *testing.T) {
	d := Decide(Review, shellReq("echo $(whoami)"), nil)
	if d.Verdict != Ask || len(d.Remember) != 0 || len(d.Choices) != 2 {
		t.Fatalf("%+v", d)
	}
}

func TestParsePreset(t *testing.T) {
	for _, value := range []string{"", "auto", "read-only"} {
		if p, err := ParsePreset(value); err != nil || p != ReadOnly {
			t.Fatal(value, p, err)
		}
	}
	if _, err := ParsePreset("review"); err == nil {
		t.Fatal("internal plugin policy exposed as a session preset")
	}
	if _, err := ParsePreset("yolo"); err == nil {
		t.Fatal("want error")
	}
}
