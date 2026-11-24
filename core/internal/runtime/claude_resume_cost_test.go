package runtime

import (
	"context"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/agent"
	"github.com/Ady-JK/paladin/core/internal/claude"
	"github.com/Ady-JK/paladin/core/internal/session"
)

func TestClaudeNewTransportBillsEqualNativeTotals(t *testing.T) {
	rt, _ := claudeRuntime(t)
	id, cwd := openClaude(t, rt)
	sess, err := rt.transcripts.Load(cwd, id)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		run := &claudeRun{r: rt, th: &claudeThread{r: rt}, sess: sess, req: agent.Request{SessionID: id, Cwd: cwd}, ctx: context.Background(), model: "claude-haiku-5-5", tx: make(chan agent.ChatChunk, 8)}
		if err := run.persistClaudeUsage(claude.Decode([]byte(`{"total_cost_usd":0.01,"usage":{"input_tokens":1,"output_tokens":1}}`))); err != nil {
			t.Fatal(err)
		}
	}
	ledger, err := session.ReadLedger(sess.UsagePath())
	if err != nil || ledger.SpendUsd() < 0.0199 || ledger.SpendUsd() > 0.0201 {
		t.Fatalf("restart spend=%v err=%v", ledger.SpendUsd(), err)
	}
}
