package permission

import (
	"context"
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/sandbox"
)

func blockedAt(path string) Blocked {
	return Blocked{
		Policy: &sandbox.Policy{Mode: sandbox.ModeWorkspaceWrite, Writable: []string{"/ws"}},
		Output: "npm error EPERM: operation not permitted, mkdir '" + path + "'",
	}
}

func escalate(t *testing.T, g *Gate, cmd string, b Blocked, reply string) (Escalation, *recorder) {
	t.Helper()
	rec := &recorder{}
	answer(t, g.Broker, "s1", "c1", reply)
	return g.Escalate(context.Background(), "c1", "shell", args(map[string]any{"cmd": cmd}), b, rec.notify), rec
}

func TestEscalateOffersToGrantTheBlockedFolder(t *testing.T) {
	g := newGate(t, Auto)
	esc, rec := escalate(t, g, "npm i -g x", blockedAt("/opt/tool/lib/x"), ReplySession)
	card := rec.evs[0]
	want := []string{ReplySession, ReplyFolder, ReplyGlobal, ReplyOnce, ReplyDeny}
	if strings.Join(card.Choices, ",") != strings.Join(want, ",") {
		t.Fatalf("choices %v", card.Choices)
	}
	if len(card.Remember) != 1 || card.Remember[0] != (Rule{Tool: ToolWrite, Pattern: "/opt/tool/lib", Action: ActionAllow}) {
		t.Fatalf("remember %+v", card.Remember)
	}
	if !esc.Run || esc.Sandbox == nil || esc.Sandbox.Mode != sandbox.ModeWorkspaceWrite ||
		strings.Join(esc.Sandbox.Writable, ",") != "/ws" || strings.Join(esc.Sandbox.Granted, ",") != "/opt/tool/lib" {
		t.Fatalf("rerun sandbox %+v", esc.Sandbox)
	}
	if rules, _ := g.Store.Rules(ScopeSession, "/ws", "s1"); len(rules) != 1 || rules[0].Pattern != "/opt/tool/lib" {
		t.Fatalf("session rules %+v", rules)
	}
}

func TestEscalateGrantScopes(t *testing.T) {
	g := newGate(t, Auto)
	escalate(t, g, "make install", blockedAt("/opt/a/b"), ReplyFolder)
	if rules, _ := g.Store.Rules(ScopeFolder, "/ws", ""); len(rules) != 1 || rules[0].Pattern != "/opt/a" {
		t.Fatalf("folder rules %+v", rules)
	}
	escalate(t, g, "make install", blockedAt("/opt/c/d"), ReplyGlobal)
	if rules := g.Store.Effective("/elsewhere", "other"); len(rules) != 1 || rules[0].Pattern != "/opt/c" {
		t.Fatalf("global rules %+v", rules)
	}
}

func TestEscalateOnceRunsUnconfined(t *testing.T) {
	g := newGate(t, Auto)
	esc, _ := escalate(t, g, "npm i -g x", blockedAt("/opt/tool/lib/x"), ReplyOnce)
	if !esc.Run || esc.Sandbox != nil {
		t.Fatalf("%+v", esc)
	}
	if rules := g.Store.Effective("/ws", "s1"); len(rules) != 0 {
		t.Fatalf("once saved rules %+v", rules)
	}
}

func TestEscalateDestructiveNeverGrants(t *testing.T) {
	g := newGate(t, Auto)
	_, rec := escalate(t, g, "rm -rf /opt/tool/lib", blockedAt("/opt/tool/lib/x"), ReplyDeny)
	if c := rec.evs[0]; len(c.Choices) != 2 || len(c.Remember) != 0 {
		t.Fatalf("%+v", c)
	}
}

func TestEscalateGrantsNarrowly(t *testing.T) {
	homeDir = func() (string, error) { return "/Users/me", nil }
	t.Cleanup(func() { homeDir = defaultHomeDir })
	for path, want := range map[string]string{
		"/Users/me/.npmrc":            "/Users/me/.npmrc",
		"/etc/hosts":                  "/etc/hosts",
		"/Users/other":                "/Users/other",
		"/Users/me/.nvm/versions/x/y": "/Users/me/.nvm/versions/x",
	} {
		g := newGate(t, Auto)
		_, rec := escalate(t, g, "x", blockedAt(path), ReplyDeny)
		if c := rec.evs[0]; len(c.Remember) != 1 || c.Remember[0].Pattern != want {
			t.Errorf("%s: remember %+v", path, c.Remember)
		}
	}
}

func TestEscalateSkipsGrantForAlreadyWritablePath(t *testing.T) {
	g := newGate(t, Auto)
	_, rec := escalate(t, g, "x", blockedAt("/ws/sub/f"), ReplyDeny)
	if c := rec.evs[0]; len(c.Remember) != 0 || len(c.Choices) != 2 {
		t.Fatalf("%+v", c)
	}
}

func TestWriteRulesBecomeSandboxGrants(t *testing.T) {
	g := newGate(t, ReadOnly)
	_ = g.Store.Add(ScopeGlobal, "", "",
		Rule{Tool: ToolWrite, Pattern: "/opt/g", Action: ActionAllow},
		Rule{Tool: ToolWrite, Pattern: "*", Action: ActionAllow},
		Rule{Tool: ToolWrite, Pattern: "/opt/denied", Action: ActionAllow},
		Rule{Tool: ToolWrite, Pattern: "/opt", Action: ActionDeny})
	_ = g.Store.Add(ScopeSession, "/ws", "s1", Rule{Tool: ToolWrite, Pattern: "/srv/s", Action: ActionAllow})
	auth := g.Authorize(context.Background(), "c1", "shell", args(map[string]any{"cmd": "ls"}), func(Approval) {})
	if auth.Sandbox == nil || auth.Sandbox.Mode != sandbox.ModeReadOnly || strings.Join(auth.Sandbox.Granted, ",") != "/srv/s" {
		t.Fatalf("%+v", auth.Sandbox)
	}
}

func TestWriteCardOffersGlobal(t *testing.T) {
	d := Decide(Auto, Request{Tool: "write", Kind: KindWrite, Path: "/etc/x", Outside: true}, nil)
	if strings.Join(d.Choices, ",") != "once,session,folder,global,deny" {
		t.Fatalf("%v", d.Choices)
	}
	if err := NewBroker().Resolve("s", "x", ReplyGlobal); err == nil || !strings.Contains(err.Error(), "no pending") {
		t.Fatalf("global must be a valid reply: %v", err)
	}
}
