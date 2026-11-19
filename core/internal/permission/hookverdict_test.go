package permission

import (
	"context"
	"testing"
)

func TestHookAllowSkipsThePrompt(t *testing.T) {
	g := newGate(t, Review)
	rec := &recorder{}
	ctx := WithHookVerdict(context.Background(), HookAllow)
	auth := g.Authorize(ctx, "c1", "shell", args(map[string]any{"cmd": "npm install"}), rec.notify)
	if !auth.Run || rec.count() != 0 {
		t.Fatalf("%+v events=%d", auth, rec.count())
	}
}

func TestHookAllowNeverBeatsADenyRule(t *testing.T) {
	g := newGate(t, Review)
	_ = g.Store.Add(ScopeSession, "/ws", "s1", Rule{Tool: ToolShell, Pattern: "npm install", Action: ActionDeny})
	ctx := WithHookVerdict(context.Background(), HookAllow)
	if auth := g.Authorize(ctx, "c1", "shell", args(map[string]any{"cmd": "npm install"}), (&recorder{}).notify); auth.Run {
		t.Fatalf("deny rule bypassed: %+v", auth)
	}
}

func TestHookAskForcesAPrompt(t *testing.T) {
	g := newGate(t, Review)
	rec := &recorder{}
	answer(t, g.Broker, "s1", "c1", ReplyOnce)
	ctx := WithHookVerdict(context.Background(), HookAsk)
	auth := g.Authorize(ctx, "c1", "shell", args(map[string]any{"cmd": "ls"}), rec.notify)
	if !auth.Run || rec.count() == 0 {
		t.Fatalf("%+v events=%d", auth, rec.count())
	}
	card := rec.evs[0]
	if len(card.Choices) != 2 || card.Reasons[0] != "a hook asked for confirmation" {
		t.Fatalf("card %+v", card)
	}
}
