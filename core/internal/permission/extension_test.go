package permission

import (
	"context"
	"testing"
)

// Extension tools run local code with no sandbox: each call asks like an
// MCP tool, rules remember the tool by name, and deny rules win.
func TestExtensionToolsAsk(t *testing.T) {
	g := newGate(t, Auto)
	g.Extension = func(name string) bool { return name == "jira_lookup" }
	rec := &recorder{}
	answer(t, g.Broker, "s1", "c1", ReplySession)
	auth := g.Authorize(context.Background(), "c1", "jira_lookup", args(map[string]any{"q": "PAL-1"}), rec.notify)
	if !auth.Run || len(rec.evs) == 0 {
		t.Fatalf("auth %+v events %d", auth, len(rec.evs))
	}
	card := rec.evs[0]
	if card.Kind != KindExtension || card.Tool != "jira_lookup" || card.Args == "" || len(card.Remember) != 1 ||
		card.Remember[0] != (Rule{Tool: ToolExtension, Pattern: "jira_lookup", Action: ActionAllow}) {
		t.Fatalf("card %+v", card)
	}
	quiet := &recorder{}
	if auth := g.Authorize(context.Background(), "c2", "jira_lookup", args(map[string]any{}), quiet.notify); !auth.Run || quiet.count() != 0 {
		t.Fatalf("the session rule covers it: %+v %d", auth, quiet.count())
	}
	// Other tools are not extensions.
	if auth := g.Authorize(context.Background(), "c3", "todo", args(map[string]any{}), quiet.notify); !auth.Run || quiet.count() != 0 {
		t.Fatal("a built-in was gated as an extension")
	}
	req := Request{Tool: "jira_lookup", Kind: KindExtension}
	if d := Decide(FullAccess, req, nil); d.Verdict != Allow {
		t.Fatalf("full access: %+v", d)
	}
	if d := Decide(FullAccess, req, []Rule{{Tool: ToolExtension, Pattern: "*", Action: ActionDeny}}); d.Verdict != Deny {
		t.Fatalf("deny wins: %+v", d)
	}
	for _, p := range []string{"jira_lookup", "*"} {
		if err := (Rule{Tool: ToolExtension, Pattern: p, Action: ActionAllow}).validate(); err != nil {
			t.Errorf("%q: %v", p, err)
		}
	}
	if err := (Rule{Tool: ToolExtension, Pattern: "a/b", Action: ActionAllow}).validate(); err == nil {
		t.Fatal("extension rules name one tool")
	}
}
