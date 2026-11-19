package runtime

import (
	"strings"
	"testing"
	"time"

	"github.com/Ady-JK/paladin/core/internal/agent"
	"github.com/Ady-JK/paladin/core/internal/harness"
	"github.com/Ady-JK/paladin/core/internal/session"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

// historyRow finds the stored row of a tool call by its name.
func historyRow(t *testing.T, rt *AgentRuntime, id, cwd, tool string) (session.HistoryMessage, bool) {
	t.Helper()
	rows, err := rt.SessionHistory(id, cwd)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Role == "tool" && row.Name != nil && *row.Name == tool {
			return row, true
		}
	}
	return session.HistoryMessage{}, false
}

// waitHistory polls until pred holds for the named tool's row.
func waitHistory(t *testing.T, rt *AgentRuntime, id, cwd, tool, what string, pred func(session.HistoryMessage) bool) session.HistoryMessage {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if row, ok := historyRow(t, rt, id, cwd, tool); ok && pred(row) {
			return row
		}
		time.Sleep(25 * time.Millisecond)
	}
	row, _ := historyRow(t, rt, id, cwd, tool)
	t.Fatalf("timed out waiting for %s; row = %+v", what, row)
	return row
}

func codexRequest(id, cwd string) agent.Request {
	provider, model := HarnessCodex, "fake-luna"
	return agent.Request{SessionID: id, Cwd: cwd, Provider: &provider, Model: &model}
}

func TestCodexAsyncQuestionBecomesAnInlineCardAndTheTurnContinues(t *testing.T) {
	rt, log, _ := codexRuntime(t, "async-question")
	id, cwd := openCodex(t, rt)
	final := <-submitCodex(t, rt, id, cwd, "ask me")
	if final.Error != "" {
		t.Fatalf("final %+v", final)
	}
	var card *agent.ToolActivity
	for _, e := range log.all() {
		if e.Tool != nil && e.Tool.Name == harness.CardQuestion {
			card = e.Tool
		}
	}
	if card == nil || card.Status != session.ToolOK || !strings.Contains(card.Result, `"pending"`) || !strings.Contains(card.Arguments, `"Pick"`) {
		t.Fatalf("question card %+v", card)
	}
	// Nothing was asked to wait: the turn went on after the question.
	if !strings.Contains(final.Reply, "Carrying on.") || strings.Contains(final.Reply, "- A") {
		t.Fatalf("reply %q: the question belongs in its card, not in the text", final.Reply)
	}
	row, ok := historyRow(t, rt, id, cwd, harness.CardQuestion)
	if !ok || !strings.Contains(row.Content, "pending") {
		t.Fatalf("stored row %+v", row)
	}
}

func TestCodexAnswerAfterTheTurnStartsATurnWithTheReply(t *testing.T) {
	rt, log, fake := codexRuntime(t, "async-question")
	id, cwd := openCodex(t, rt)
	<-submitCodex(t, rt, id, cwd, "ask me")
	callID := ""
	for _, e := range log.all() {
		if e.Tool != nil && e.Tool.Name == harness.CardQuestion {
			callID = e.Tool.ID
		}
	}
	answers := []harness.Answer{{Question: "Pick", Answer: "A"}, {Question: "Why?", Answer: "because"}}
	info, err := rt.AnswerQuestion(codexRequest(id, cwd), callID, answers)
	if err != nil {
		t.Fatal(err)
	}
	rt.hub.WaitIdle(id, 5*time.Second)
	_ = info
	logged := fakeLog(t, fake)
	if !strings.Contains(logged, `text=<send_user_message_question_reply>\n[{"answer":"A","question":"Pick","questionItemId":"[\"request_user_input_async\",\"`+callID+`\",0]"}`) {
		t.Fatalf("Codex was not sent the reply envelope:\n%s", logged)
	}
	row := waitHistory(t, rt, id, cwd, harness.CardQuestion, "the first card to read answered", func(r session.HistoryMessage) bool {
		return strings.Contains(r.Content, "answered")
	})
	if !strings.Contains(row.Content, "because") {
		t.Fatalf("answers missing from the card: %s", row.Content)
	}
	rows, _ := rt.SessionHistory(id, cwd)
	shown := false
	for _, r := range rows {
		if r.Role == "user" && strings.Contains(r.Content, "> Pick\n\nA\n\n> Why?\n\nbecause") {
			shown = true
		}
	}
	if !shown {
		t.Fatalf("the reply should read as a quoted answer in the conversation: %+v", rows)
	}
}

func TestCodexAnswerDuringTheTurnSteersIt(t *testing.T) {
	rt, log, fake := codexRuntime(t, "async-question-hold")
	id, cwd := openCodex(t, rt)
	done := submitCodex(t, rt, id, cwd, "ask me")
	events := log.waitFor(t, "the question card", func(evs []TurnEvent) bool {
		for _, e := range evs {
			if e.Tool != nil && e.Tool.Name == harness.CardQuestion {
				return true
			}
		}
		return false
	})
	callID := ""
	for _, e := range events {
		if e.Tool != nil && e.Tool.Name == harness.CardQuestion {
			callID = e.Tool.ID
		}
	}
	// The turn is still open, waiting for nothing: the answer goes into it.
	if _, err := rt.AnswerQuestion(codexRequest(id, cwd), callID, []harness.Answer{{Question: "Pick", Answer: "B"}}); err != nil {
		t.Fatal(err)
	}
	if final := <-done; final.Error != "" {
		t.Fatalf("final %+v", final)
	}
	logged := fakeLog(t, fake)
	if !strings.Contains(logged, "turn/steer text=<send_user_message_question_reply>") || strings.Count(logged, "turn/start") != 1 {
		t.Fatalf("the answer should steer the running turn, not start another:\n%s", logged)
	}
	settled := false
	for _, e := range log.all() {
		if e.Tool != nil && e.Tool.Name == harness.CardQuestion && strings.Contains(e.Tool.Result, "answered") {
			settled = true
		}
	}
	if !settled {
		t.Fatal("the card never read answered while the turn ran")
	}
	row, _ := historyRow(t, rt, id, cwd, harness.CardQuestion)
	if !strings.Contains(row.Content, `"B"`) {
		t.Fatalf("stored row %s", row.Content)
	}
}

func TestCodexBackgroundCommandIsRunningNotFailedWhenTheTurnEnds(t *testing.T) {
	rt, log, _ := codexRuntime(t, "background")
	id, cwd := openCodex(t, rt)
	if final := <-submitCodex(t, rt, id, cwd, "start it"); final.Error != "" {
		t.Fatalf("final %+v", final)
	}
	row, ok := historyRow(t, rt, id, cwd, "shell")
	if !ok {
		t.Fatal("no shell card")
	}
	if row.Status != nil && *row.Status == "failed" {
		t.Fatalf("a command still running was recorded as failed: %+v", row)
	}
	if !strings.Contains(row.Content, `"running"`) || row.Arguments == nil || !strings.Contains(*row.Arguments, `"background":true`) {
		t.Fatalf("the card should read running in background: %+v", row)
	}
	// Later Codex reports how it ended: the same card gets the real outcome.
	row = waitHistory(t, rt, id, cwd, "shell", "the command's real outcome", func(r session.HistoryMessage) bool {
		return strings.Contains(r.Content, `"exit_code":0`)
	})
	if !strings.Contains(row.Content, "bgdone") || (row.Status != nil && *row.Status == "failed") {
		t.Fatalf("outcome %+v", row)
	}
	log.waitFor(t, "a refresh for the session", func(evs []TurnEvent) bool {
		for _, e := range evs {
			if e.Event == "session/refresh" && e.SessionID == id {
				return true
			}
		}
		return false
	})
}

func TestCodexBackgroundCommandThatEndsDuringAnotherTurnStillSettles(t *testing.T) {
	rt, _, _ := codexRuntime(t, "background-late")
	id, cwd := openCodex(t, rt)
	<-submitCodex(t, rt, id, cwd, "start it")
	waitHistory(t, rt, id, cwd, "shell", "the running card", func(r session.HistoryMessage) bool { return strings.Contains(r.Content, `"running"`) })
	<-submitCodex(t, rt, id, cwd, "and now?")
	row := waitHistory(t, rt, id, cwd, "shell", "the late outcome", func(r session.HistoryMessage) bool {
		return strings.Contains(r.Content, `"exit_code":0`)
	})
	if !strings.Contains(row.Content, "bgdone") {
		t.Fatalf("outcome %+v", row)
	}
}

// An older Paladin closed a command it could not follow as unknown or interrupted, or left
// it running when the turn ended. Opening the session reads Codex's own record and
// puts the real outcome on those cards; what Codex cannot vouch for reads unknown.
func TestCodexOpeningASessionRepairsCommandsItNeverSawEnd(t *testing.T) {
	rt, log, _ := codexRuntime(t, "chat")
	cwd := t.TempDir()
	opened, err := rt.OpenSessionWith("", cwd, OpenOpts{Harness: HarnessCodex, Model: "fake-luna"})
	if err != nil {
		t.Fatal(err)
	}
	id := opened.SessionID
	if err := rt.transcripts.UpdateMeta(cwd, id, func(m *transcript.Meta) error {
		m.Harness = &transcript.Harness{Name: HarnessCodex, ThreadID: "th-repair"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sess, _ := rt.transcripts.Load(cwd, id)
	msgs := []transcript.Message{transcript.UserMessage("run things")}
	for _, c := range []struct {
		id, result string
		failed     bool
	}{
		{"exec-old", `{"status":"unknown"}`, false},
		{"exec-int", `{"error":"interrupted before it finished"}`, true},
		{"exec-lost", `{"output":"","status":"running"}`, false},
		{"exec-fine", `{"output":"ok","status":"completed","exit_code":0}`, false},
	} {
		callID, name := c.id, "shell"
		msgs = append(msgs,
			transcript.Message{Role: transcript.RoleAssistant, ToolCalls: &[]transcript.ToolCall{{ID: callID, Name: name, Arguments: `{"command":"sleep 1"}`}}},
			transcript.Message{Role: transcript.RoleTool, ToolCallID: &callID, Name: &name, Content: c.result, IsError: c.failed})
	}
	if err := sess.AppendTurn(msgs); err != nil {
		t.Fatal(err)
	}
	row := func(call string) session.HistoryMessage {
		rows, _ := rt.SessionHistory(id, cwd)
		for _, r := range rows {
			if r.Role == "tool" && r.ToolCallID != nil && *r.ToolCallID == call {
				return r
			}
		}
		return session.HistoryMessage{}
	}
	deadline := time.Now().Add(5 * time.Second)
	// The repair settles the rows in no fixed order: wait for all three.
	repaired := func() bool {
		return strings.Contains(row("exec-old").Content, `"exit_code":0`) &&
			strings.Contains(row("exec-int").Content, `"exit_code":3`) &&
			strings.Contains(row("exec-lost").Content, `"unknown"`)
	}
	for time.Now().Before(deadline) && !repaired() {
		time.Sleep(25 * time.Millisecond)
	}
	if got := row("exec-old"); !strings.Contains(got.Content, "done") || !strings.Contains(got.Content, `"exit_code":0`) || (got.Status != nil && *got.Status == "failed") {
		t.Fatalf("exec-old: %+v", got)
	}
	if got := row("exec-int"); !strings.Contains(got.Content, `"exit_code":3`) || got.Status == nil || *got.Status != "failed" {
		t.Fatalf("exec-int: %+v", got)
	}
	if got := row("exec-lost"); !strings.Contains(got.Content, `"unknown"`) {
		t.Fatalf("a command nobody can follow any more should read unknown: %+v", got)
	}
	if got := row("exec-fine"); !strings.Contains(got.Content, `"exit_code":0`) || !strings.Contains(got.Content, `"ok"`) {
		t.Fatalf("a settled card was touched: %+v", got)
	}
	log.waitFor(t, "a refresh", func(evs []TurnEvent) bool {
		for _, e := range evs {
			if e.Event == "session/refresh" {
				return true
			}
		}
		return false
	})
}

// A thread started in another Codex client keeps its questions and their answers
// when it is imported: the question is a card, answered by the later reply, and the
// reply reads as a quoted answer.
func TestCodexImportKeepsAsyncQuestionsAsCards(t *testing.T) {
	rt, _, _ := codexRuntime(t, "chat")
	cwd := t.TempDir()
	t.Setenv("PALADIN_FAKE_CODEX_CWD", cwd)
	t.Setenv("PALADIN_FAKE_CODEX_ASYNC", "1")
	opened, err := rt.ImportCodexThread("th-async", "")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := rt.SessionHistory(opened.SessionID, cwd)
	if err != nil {
		t.Fatal(err)
	}
	var card *session.HistoryMessage
	var texts []string
	for i, r := range rows {
		if r.Role == "tool" && r.Name != nil && *r.Name == harness.CardQuestion {
			card = &rows[i]
		}
		if r.Role == "assistant" {
			texts = append(texts, r.Content)
		}
	}
	if card == nil || !strings.Contains(card.Content, `"answered"`) || !strings.Contains(card.Content, `"B"`) {
		t.Fatalf("the question should be an answered card: %+v", card)
	}
	for _, text := range texts {
		if strings.Contains(text, "- A") {
			t.Fatalf("the question was imported as plain text: %q", text)
		}
	}
	quoted := false
	for _, r := range rows {
		if r.Role == "user" && strings.Contains(r.Content, "> Pick\n\nB") {
			quoted = true
		}
	}
	if !quoted {
		t.Fatalf("the reply should read as a quoted answer: %+v", rows)
	}
}
