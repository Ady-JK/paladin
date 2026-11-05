package permission

import (
	"context"
	"testing"
)

// Extension tools run local code with no sandbox: each call asks, a rule
// can name the plugin, one of its tools or everything, and deny rules
// win. Rules now speak the new grammar: "<plugin>", "<plugin>/<tool>",
// "<plugin>/*" or "*"; a bare name is a plugin name, not a tool name.
func TestExtensionRulesLetACallRun(t *testing.T) {
	g := newGate(t, Auto)
	g.Extension = func(name string) (ExtensionRef, bool) {
		if name == "extension__jira__lookup" {
			return ExtensionRef{Plugin: "jira", Tool: "lookup"}, true
		}
		return ExtensionRef{}, false
	}
	rec := &recorder{}
	answer(t, g.Broker, "s1", "c1", ReplySession)
	auth := g.Authorize(context.Background(), "c1", "extension__jira__lookup", args(map[string]any{"q": "PAL-1"}), rec.notify)
	if !auth.Run || len(rec.evs) == 0 {
		t.Fatalf("auth %+v events %d", auth, len(rec.evs))
	}
	card := rec.evs[0]
	want := Rule{Tool: ToolExtension, Pattern: "jira/lookup", Action: ActionAllow}
	if card.Kind != KindExtension || card.Tool != "extension__jira__lookup" || card.Args == "" ||
		len(card.Remember) != 1 || card.Remember[0] != want {
		t.Fatalf("card %+v", card)
	}
	// The session rule covers the same tool, and only that tool.
	quiet := &recorder{}
	if auth := g.Authorize(context.Background(), "c2", "extension__jira__lookup", args(map[string]any{}), quiet.notify); !auth.Run || quiet.count() != 0 {
		t.Fatalf("the session rule covers it: %+v %d", auth, quiet.count())
	}
	g.Extension = func(name string) (ExtensionRef, bool) {
		if name == "extension__jira__delete" {
			return ExtensionRef{Plugin: "jira", Tool: "delete"}, true
		}
		return ExtensionRef{}, false
	}
	asked := &recorder{}
	answer(t, g.Broker, "s1", "c3", ReplyDeny)
	if auth := g.Authorize(context.Background(), "c3", "extension__jira__delete", args(map[string]any{}), asked.notify); auth.Run {
		t.Fatal("a rule for jira/lookup must not cover jira/delete")
	}
	if asked.count() == 0 {
		t.Fatal("the other tool should have asked")
	}
	// Built-ins are never gated as extensions.
	builtin := &recorder{}
	if auth := g.Authorize(context.Background(), "c4", "todo", args(map[string]any{}), builtin.notify); !auth.Run || builtin.count() != 0 {
		t.Fatal("a built-in was gated as an extension")
	}
}

func extensionReq(plugin, tool string) Request {
	return Request{Tool: "extension__" + plugin + "__" + tool, Kind: KindExtension, ExtensionPlugin: plugin, ExtensionTool: tool}
}

func TestExtensionRuleMatching(t *testing.T) {
	cases := []struct {
		pattern string
		allow   bool
	}{
		{"*", true},
		{"jira", true},
		{"jira/*", true},
		{"jira/lookup", true},
		{"jira/delete", false},
		{"other", false},
		{"other/*", false},
		{"other/lookup", false},
		{"lookup", false}, // a bare name is a plugin, not a tool
	}
	req := extensionReq("jira", "lookup")
	for _, tc := range cases {
		d := Decide(Auto, req, []Rule{{Tool: ToolExtension, Pattern: tc.pattern, Action: ActionAllow}})
		if (d.Verdict == Allow) != tc.allow {
			t.Errorf("allow %q for jira/lookup: %+v", tc.pattern, d)
		}
		if verdict := Decide(FullAccess, req, []Rule{{Tool: ToolExtension, Pattern: tc.pattern, Action: ActionDeny}}); (verdict.Verdict == Deny) != tc.allow {
			t.Errorf("deny %q for jira/lookup: %+v", tc.pattern, verdict)
		}
	}
	// Deny beats allow, even at full access.
	d := Decide(FullAccess, req, []Rule{
		{Tool: ToolExtension, Pattern: "jira/*", Action: ActionDeny},
		{Tool: ToolExtension, Pattern: "*", Action: ActionAllow},
	})
	if d.Verdict != Deny {
		t.Fatalf("deny wins: %+v", d)
	}
}

// A legacy loose file has no plugin directory, so its rules keep naming
// the exposed tool.
func TestLegacyExtensionRuleStillMatches(t *testing.T) {
	req := Request{Tool: "extension__hello__hello", Kind: KindExtension}
	d := Decide(FullAccess, req, []Rule{{Tool: ToolExtension, Pattern: "extension__hello__hello", Action: ActionAllow}})
	if d.Verdict != Allow {
		t.Fatalf("legacy rule: %+v", d)
	}
	if d := Decide(Auto, req, nil); d.Verdict != Ask || len(d.Remember) != 1 ||
		d.Remember[0].Pattern != "extension__hello__hello" {
		t.Fatalf("legacy remember: %+v", d)
	}
}

func TestExtensionRuleValidation(t *testing.T) {
	for _, p := range []string{"*", "jira", "jira/*", "jira/lookup", "a-b_c/1tool?"} {
		err := (Rule{Tool: ToolExtension, Pattern: p, Action: ActionAllow}).validate()
		if p == "a-b_c/1tool?" {
			if err == nil {
				t.Errorf("%q should be refused", p)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: %v", p, err)
		}
	}
	for _, p := range []string{"", "/", "a/", "/b", "a/b/c", "a b", "../etc", "*/*", "a/*/b"} {
		if err := (Rule{Tool: ToolExtension, Pattern: p, Action: ActionAllow}).validate(); err == nil {
			t.Errorf("%q should be refused", p)
		}
	}
}
