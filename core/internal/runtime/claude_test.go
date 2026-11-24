package runtime

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Ady-JK/paladin/core/internal/agent"
	"github.com/Ady-JK/paladin/core/internal/atomicfile"
	"github.com/Ady-JK/paladin/core/internal/claude"
	"github.com/Ady-JK/paladin/core/internal/harness/claudeadapter"
)

func claudeRuntime(t *testing.T) (*AgentRuntime, *eventLog) {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "claude-fake")
	if b, err := exec.Command("go", "build", "-o", bin, "../claude/testdata/fake").CombinedOutput(); err != nil {
		t.Fatalf("fake CLI: %v: %s", err, b)
	}
	t.Setenv(claude.BinEnv, bin)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	rt, _ := testRuntime(t)
	log := newEventLog()
	rt.SetTurnSink(log.sink)
	t.Cleanup(func() { rt.ShutdownTurns(3 * time.Second) })
	return rt, log
}
func openClaude(t *testing.T, rt *AgentRuntime) (string, string) {
	t.Helper()
	cwd := t.TempDir()
	opened, err := rt.OpenSessionWith("", cwd, OpenOpts{Harness: HarnessClaude, Model: "claude-haiku-5-5"})
	if err != nil {
		t.Fatal(err)
	}
	if opened.Harness != HarnessClaude || opened.Provider != HarnessClaude {
		t.Fatalf("wrong harness %+v", opened)
	}
	return opened.SessionID, cwd
}
func submitClaude(t *testing.T, rt *AgentRuntime, id, cwd, message, preset string) <-chan TurnInfo {
	t.Helper()
	provider, model := HarnessClaude, "claude-haiku-5-5"
	_, done, err := rt.SubmitTurn(agent.Request{SessionID: id, Cwd: cwd, Message: message, Provider: &provider, Model: &model, Permissions: preset}, SubmitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	return done
}
func TestClaudePersistsStreamsAndReusesSession(t *testing.T) {
	rt, _ := claudeRuntime(t)
	id, cwd := openClaude(t, rt)
	for _, msg := range []string{"hello", "again"} {
		final := awaitTurn(t, submitClaude(t, rt, id, cwd, msg, "full-access"))
		if final.Error != "" || final.Reply != "hello world" || !final.Persisted {
			t.Fatalf("final=%+v", final)
		}
	}
	hist, err := rt.SessionHistory(id, cwd)
	if err != nil || len(hist) != 4 {
		t.Fatalf("history rows=%d err=%v", len(hist), err)
	}
	sess, _ := rt.transcripts.Load(cwd, id)
	if sess.Meta.Harness.ThreadID == "" {
		t.Fatal("missing durable Claude session ID")
	}
	usage, err := rt.SessionUsage(id, cwd)
	if err != nil || usage.ContextWindow != 200000 || usage.CompletionTokens != 6 {
		t.Fatalf("usage=%+v err=%v", usage, err)
	}
	if st := rt.ClaudeStatus(true); !st.LoggedIn || len(st.Models) != 1 {
		t.Fatalf("status=%+v", st)
	}
}
func TestClaudeFullAccessAutoApproves(t *testing.T) {
	rt, log := claudeRuntime(t)
	id, cwd := openClaude(t, rt)
	final := awaitTurn(t, submitClaude(t, rt, id, cwd, "permission-test", "full-access"))
	if final.Error != "" {
		t.Fatalf("final=%+v", final)
	}
	for _, ev := range log.all() {
		if ev.Approval != nil && ev.Approval.State == "pending" {
			t.Fatalf("unexpected pending approval in full access: %+v", ev.Approval)
		}
	}
	if _, err := os.Stat(filepath.Join(cwd, "test.txt")); err != nil {
		t.Fatal("auto-approved write did not run")
	}
}
func TestClaudeDiscoveryImportAndTombstones(t *testing.T) {
	rt, _ := claudeRuntime(t)
	root := os.Getenv("CLAUDE_CONFIG_DIR")
	id := "c0de0c0d-5e55-4c0a-a111-111111111111"
	cwd := t.TempDir()
	rows := []map[string]any{{"type": "user", "uuid": "u1", "parentUuid": nil, "cwd": cwd, "message": map[string]any{"content": "saved Claude conversation"}}, {"type": "assistant", "uuid": "a1", "parentUuid": "u1", "message": map[string]any{"id": "m1", "model": "claude-haiku-5-5", "content": []any{map[string]any{"type": "text", "text": "saved answer"}}}}}
	var data []byte
	for _, row := range rows {
		b, _ := json.Marshal(row)
		data = append(data, append(b, '\n')...)
	}
	if err := os.MkdirAll(filepath.Join(root, "projects", "project"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := atomicfile.Write(filepath.Join(root, "projects", "project", id+".jsonl"), data); err != nil {
		t.Fatal(err)
	}
	rt.discoverClaude()
	sessions, err := rt.ListSessions("", 100)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("discovered=%d err=%v", len(sessions), err)
	}
	opened, err := rt.ImportClaudeSession(id, cwd)
	if err != nil || opened.SessionID != sessions[0].SessionID {
		t.Fatalf("duplicate import=%+v err=%v", opened, err)
	}
	history, err := rt.SessionHistory(opened.SessionID, cwd)
	if err != nil || len(history) != 2 {
		t.Fatalf("imported history=%d err=%v", len(history), err)
	}
	if _, err = rt.DeleteSession(opened.SessionID, cwd); err != nil {
		t.Fatal(err)
	}
	rt.discoverClaude()
	sessions, _ = rt.ListSessions("", 100)
	if len(sessions) != 0 {
		t.Fatal("discovery resurrected deleted link")
	}
}
func TestClaudeBackgroundTaskSettlesAfterRootTurn(t *testing.T) {
	rt, _ := claudeRuntime(t)
	id, cwd := openClaude(t, rt)
	final := awaitTurn(t, submitClaude(t, rt, id, cwd, "hello", "full-access"))
	if final.Error != "" {
		t.Fatal(final.Error)
	}
	th := rt.claudeThreads[id]
	started := claude.Decode([]byte(`{"type":"system","subtype":"task_started","task_id":"bg","description":"Background build","task_type":"local_bash"}`))
	run := &claudeRun{r: rt, th: th, req: agent.Request{SessionID: id, Cwd: cwd}, ctx: context.Background(), tx: make(chan agent.ChatChunk, 8), p: claudeadapter.NewProjection()}
	if err := th.taskEvent(started, run); err != nil {
		t.Fatal(err)
	}
	sess, err := rt.transcripts.Load(cwd, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendTurn(run.p.Records); err != nil {
		t.Fatal(err)
	}
	completed := claude.Decode([]byte(`{"type":"system","subtype":"task_notification","task_id":"bg","status":"completed","summary":"build passed"}`))
	if err := th.taskEvent(completed, nil); err != nil {
		t.Fatal(err)
	}
	history, err := sess.History()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, message := range history {
		if message.ToolCallID != nil && *message.ToolCallID == "claude-task-bg" && strings.Contains(message.Content, "completed") {
			found = true
		}
	}
	if !found {
		t.Fatal("background task stayed running after completion")
	}
}

func TestLiveClaudeRuntimeHaiku(t *testing.T) {
	if os.Getenv("PALADIN_CLAUDE_LIVE") != "1" {
		t.Skip("opt-in billed Haiku 5.5 integration")
	}
	rt, _ := testRuntime(t)
	log := newEventLog()
	rt.SetTurnSink(log.sink)
	t.Cleanup(func() { rt.ShutdownTurns(5 * time.Second) })
	id, cwd := openClaude(t, rt)
	// Use only Haiku 5.5, including follow-ups; never the CLI's default model.
	final := awaitTurn(t, submitClaude(t, rt, id, cwd, "Reply exactly PALADIN_HAIKU_INTEGRATED. Do not use tools.", "read-only"))
	if final.Error != "" || !strings.Contains(final.Reply, "PALADIN_HAIKU_INTEGRATED") || !final.Persisted {
		t.Fatalf("live turn=%+v", final)
	}
	rt.SetTurnSink(func(ev TurnEvent) {
		log.sink(ev)
		if ev.Approval != nil && ev.Approval.State == "pending" {
			_, _ = rt.AnswerApproval(id, ev.Approval.ID, "once")
		}
	})
	final = awaitTurn(t, submitClaude(t, rt, id, cwd, "Use the Write tool to create live.txt containing exactly HAIKU_WRITE_OK, then reply exactly DONE. Do not use Bash or any other tool.", "full-access"))
	if final.Error != "" {
		t.Fatalf("live tool turn=%+v", final)
	}
	data, err := os.ReadFile(filepath.Join(cwd, "live.txt"))
	if err != nil || string(data) != "HAIKU_WRITE_OK" {
		t.Fatalf("live tool did not write expected content: %v", err)
	}
	history, err := rt.SessionHistory(id, cwd)
	if err != nil || len(history) < 5 {
		t.Fatalf("live tool history rows=%d err=%v", len(history), err)
	}
	rt.forgetClaudeThread(id)
	final = awaitTurn(t, submitClaude(t, rt, id, cwd, "What exact marker did I ask you to reply with in my first message? Reply with that marker only; do not use tools.", "read-only"))
	if final.Error != "" || !strings.Contains(final.Reply, "PALADIN_HAIKU_INTEGRATED") {
		t.Fatalf("resume lost native conversation: %+v", final)
	}
	fork, err := rt.ForkSession(id, cwd)
	if err != nil {
		t.Fatal(err)
	}
	final = awaitTurn(t, submitClaude(t, rt, fork.SessionID, cwd, "Repeat the marker from my very first message, and nothing else. Do not use tools.", "read-only"))
	if final.Error != "" || !strings.Contains(final.Reply, "PALADIN_HAIKU_INTEGRATED") {
		t.Fatalf("native fork lost history: %+v", final)
	}
}
