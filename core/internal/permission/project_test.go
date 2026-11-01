package permission

import (
	"context"
	"strings"
	"testing"
)

// A tool the project ships asks once for the folder; the answer is
// remembered so later files run without a card, and a refusal holds for
// the session.
func TestProjectToolsAskOncePerFolder(t *testing.T) {
	g := newGate(t, Auto)
	rec := &recorder{}
	answer(t, g.Broker, "s1", "c1", ReplyFolder)
	if !g.TrustProject(context.Background(), "c1", "node_modules/.bin/prettier", rec.notify) {
		t.Fatal("allowed for the folder")
	}
	card := rec.evs[0]
	if card.Kind != KindProject || card.Command != "node_modules/.bin/prettier" || card.Path != "/ws" ||
		strings.Join(card.Choices, ",") != "session,folder,deny" || !strings.Contains(strings.Join(card.Reasons, " "), "this project") {
		t.Fatalf("card %+v", card)
	}
	if rules, _ := g.Store.Rules(ScopeFolder, "/ws", ""); len(rules) != 1 || rules[0] != (Rule{Tool: ToolProject, Pattern: "/ws", Action: ActionAllow}) {
		t.Fatalf("folder rules %+v", rules)
	}
	quiet := &recorder{}
	if !g.TrustProject(context.Background(), "c2", ".venv/bin/ruff", quiet.notify) || len(quiet.evs) != 0 {
		t.Fatalf("asked again: %+v", quiet.evs)
	}

	g = newGate(t, Auto)
	answer(t, g.Broker, "s1", "c1", ReplyDeny)
	if g.TrustProject(context.Background(), "c1", "node_modules/.bin/prettier", (&recorder{}).notify) {
		t.Fatal("denied")
	}
	quiet = &recorder{}
	if g.TrustProject(context.Background(), "c2", "node_modules/.bin/prettier", quiet.notify) || len(quiet.evs) != 0 {
		t.Fatalf("a refusal holds for the session without asking: %+v", quiet.evs)
	}

	if !newGate(t, FullAccess).TrustProject(context.Background(), "c1", "x", (&recorder{}).notify) {
		t.Fatal("full access trusts")
	}
	if err := (Rule{Tool: ToolProject, Pattern: "relative", Action: ActionAllow}).validate(); err == nil {
		t.Fatal("project rules name an absolute folder")
	}
}
