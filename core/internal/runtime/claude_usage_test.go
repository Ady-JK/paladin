package runtime

import (
	"context"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/agent"
	"github.com/Ady-JK/paladin/core/internal/claude"
)

func TestClaudeUsageDoesNotBillSharedBlocksOrPriorTotals(t *testing.T) {
	rt, _ := claudeRuntime(t)
	id, cwd := openClaude(t, rt)
	sess, err := rt.transcripts.Load(cwd, id)
	if err != nil {
		t.Fatal(err)
	}
	th := &claudeThread{r: rt, sessionID: id, cwd: cwd}
	run := &claudeRun{r: rt, th: th, sess: sess, req: agent.Request{SessionID: id, Cwd: cwd}, ctx: context.Background(), model: "claude-haiku-5-5", tx: make(chan agent.ChatChunk, 8)}
	for range 2 {
		run.noteUsage(claude.Decode([]byte(`{"type":"assistant","message":{"id":"m1","model":"claude-haiku-5-5","usage":{"input_tokens":10,"output_tokens":5}}}`)))
	}
	result := claude.Decode([]byte(`{"type":"result","total_cost_usd":0.01,"usage":{"input_tokens":1000,"output_tokens":500},"modelUsage":{"claude-haiku-5-5":{"costUSD":0.01,"contextWindow":200000}}}`))
	if err := run.persistClaudeUsage(result); err != nil {
		t.Fatal(err)
	}
	run.noteUsage(claude.Decode([]byte(`{"type":"assistant","message":{"id":"m2","model":"claude-haiku-5-5","usage":{"input_tokens":12,"output_tokens":7}}}`)))
	result = claude.Decode([]byte(`{"type":"result","total_cost_usd":0.02,"usage":{"input_tokens":2000,"output_tokens":1000},"modelUsage":{"claude-haiku-5-5":{"costUSD":0.02,"contextWindow":200000}}}`))
	if err := run.persistClaudeUsage(result); err != nil {
		t.Fatal(err)
	}
	usage, err := rt.SessionUsage(id, cwd)
	if err != nil || usage.Calls != 2 || usage.PromptTokens != 22 || usage.CompletionTokens != 12 {
		t.Fatalf("cumulative usage duplicated: %+v err=%v", usage, err)
	}
}
