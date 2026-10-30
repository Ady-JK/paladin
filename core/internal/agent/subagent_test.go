package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/inference"
)

func TestBuildReadOnlyTemplateDropsWritersAndAddsItsPrompt(t *testing.T) {
	sess := testSession(t, "00000000-0000-4000-8000-000000000031")
	d, _, _, _ := testDeps(t, sess)
	tpl := Template{Name: "explore", ReadOnly: true, Prompt: "EXPLORE AGENT RULES"}
	a, err := tpl.Build(d, Request{SessionID: sess.Meta.SessionID, Message: "look around"})
	if err != nil {
		t.Fatal(err)
	}
	for _, def := range a.toolDefs {
		if def.Name == "write" || def.Name == "edit" {
			t.Fatalf("read-only template kept %s", def.Name)
		}
		if def.Name == "shell" && !strings.HasPrefix(def.Description, "PLAN MODE") {
			t.Fatalf("shell is not the read-only shell: %q", def.Description)
		}
	}
	if !strings.Contains(a.system, "EXPLORE AGENT RULES") {
		t.Fatal("template prompt missing from the system prompt")
	}
	if strings.Contains(a.system, "PLAN MODE (read-only)") {
		t.Fatal("a subagent's own prompt replaces the plan-mode banner")
	}
}

func TestNoticesJoinTheConversationBeforeTheNextRound(t *testing.T) {
	prov := &mockProvider{streams: []streamScript{{chunks: []inference.Chunk{inference.TextChunk("noted")}}}}
	a, sess, tx, done := testAgent(t, prov)
	defer close(done)
	delivered := false
	a.notices = func() []Notice {
		if delivered {
			return nil
		}
		delivered = true
		return []Notice{{CallID: "agent-x#notify", Name: "agent", Arguments: `{"action":"notify"}`, Content: `{"notice":"subagent finished"}`}}
	}
	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(lastUser(prov.streamMsgs[0]), "subagent finished") {
		t.Fatalf("model did not see the notice: %+v", prov.streamMsgs[0])
	}
	var tool *ToolActivity
	for _, c := range drainChunks(tx) {
		if c.Tool != nil && c.Tool.ID == "agent-x#notify" {
			tool = c.Tool
		}
	}
	if tool == nil || tool.Name != "agent" {
		t.Fatalf("notice not streamed: %+v", tool)
	}
	h, _ := sess.History()
	found := false
	for _, m := range h {
		if m.IsNotify && m.Name != nil && *m.Name == "agent" {
			found = true
		}
	}
	if !found {
		t.Fatalf("notice not persisted: %+v", h)
	}
}

func TestBudgetCountsEveryLedgerItIsGiven(t *testing.T) {
	prov := &mockProvider{streams: []streamScript{{chunks: []inference.Chunk{inference.TextChunk("x")}}}}
	a, sess, tx, done := testAgent(t, prov)
	defer close(done)
	sibling := filepath.Join(t.TempDir(), "usage.json")
	if err := os.WriteFile(sibling, []byte(`{"cost":0.7,"hasCost":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sess.UsagePath(), []byte(`{"cost":0.4,"hasCost":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	a.budget = func() float64 { return 1 }
	a.spendLedgers = func() []string { return []string{sess.UsagePath(), sibling} }
	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	if prov.calls != 0 {
		t.Fatalf("family spend 1.1 is over the 1.0 limit; got %d model calls", prov.calls)
	}
	got := budgetChunks(drainChunks(tx))
	if len(got) != 1 || got[0].Spent < 1.09 {
		t.Fatalf("notices %+v", got)
	}
}
