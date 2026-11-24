package runtime

import (
	"context"
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/agent"
	"github.com/Ady-JK/paladin/core/internal/claude"
	"github.com/Ady-JK/paladin/core/internal/harness/claudeadapter"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

func TestClaudeModernTasksUpdateActivityList(t *testing.T) {
	rt, _ := claudeRuntime(t)
	id, cwd := openClaude(t, rt)
	sess, err := rt.transcripts.Load(cwd, id)
	if err != nil {
		t.Fatal(err)
	}
	th := &claudeThread{r: rt, sessionID: id, cwd: cwd}
	p := claudeadapter.NewProjection()
	p.Tools["create"] = claudeadapter.Tool("create", "TaskCreate", claude.Decode([]byte(`{"subject":"Inspect the parser"}`)))
	p.NativeTools["create"] = "TaskCreate"
	data, err := th.todoEvent(claude.Decode([]byte(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"create","content":"Task #1 created successfully"}]}}`)), p, sess.Dir)
	if err != nil || !strings.Contains(data, "Inspect the parser") {
		t.Fatalf("create=%s err=%v", data, err)
	}
	p.Tools["update"] = claudeadapter.Tool("update", "TaskUpdate", claude.Decode([]byte(`{"taskId":"1","status":"completed"}`)))
	p.NativeTools["update"] = "TaskUpdate"
	data, err = th.todoEvent(claude.Decode([]byte(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"update","content":"Updated"}]}}`)), p, sess.Dir)
	if err != nil || !strings.Contains(data, "finished") {
		t.Fatalf("update=%s err=%v", data, err)
	}
	todos, err := rt.SessionTodos(id, cwd)
	if err != nil || len(todos) != 1 || todos[0].Status != "finished" {
		t.Fatalf("todos=%+v err=%v", todos, err)
	}
}
func TestClaudeBudgetSubtractsRecordedSpend(t *testing.T) {
	rt, _ := claudeRuntime(t)
	id, cwd := openClaude(t, rt)
	budget := 0.015
	if err := rt.transcripts.UpdateMeta(cwd, id, func(meta *transcript.Meta) error { meta.BudgetUsd = &budget; return nil }); err != nil {
		t.Fatal(err)
	}
	sess, err := rt.transcripts.Load(cwd, id)
	if err != nil {
		t.Fatal(err)
	}
	th := &claudeThread{r: rt}
	run := &claudeRun{r: rt, th: th, sess: sess, req: agent.Request{SessionID: id, Cwd: cwd}, ctx: context.Background(), model: "claude-haiku-5-5", tx: make(chan agent.ChatChunk, 8)}
	if err := run.persistClaudeUsage(claude.Decode([]byte(`{"total_cost_usd":0.01,"usage":{"input_tokens":1,"output_tokens":1}}`))); err != nil {
		t.Fatal(err)
	}
	remaining, err := rt.claudeBudget(sess)
	if err != nil || remaining < 0.0049 || remaining > 0.0051 {
		t.Fatalf("remaining=%v err=%v", remaining, err)
	}
}
