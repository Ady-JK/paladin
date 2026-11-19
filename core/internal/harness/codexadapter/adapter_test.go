package codexadapter

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/codex"
	"github.com/Ady-JK/paladin/core/internal/harness"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

var adapter Adapter

func item(t *testing.T, doc string) codex.Raw {
	t.Helper()
	var raw codex.Raw
	if err := json.Unmarshal([]byte(doc), &raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

// What newer Codex sends for request_user_input_async: an agent message that does not
// end the turn, carrying the questions.
const asyncItem = `{"type":"agentMessage","id":"call_5b86","delivery":"async","text":"Pick an option\n- A\n- B\n- C","questions":[{"title":"Pick an option","options":["A","B","C"]},{"title":"Anything else?","options":null}]}`

func TestAnAsyncAgentMessageBecomesAQuestionCard(t *testing.T) {
	card, ok := QuestionCard(item(t, asyncItem))
	if !ok || card.ID != "call_5b86" || card.Name != harness.CardQuestion {
		t.Fatalf("card = %+v, %v", card, ok)
	}
	var args struct{ Questions []harness.Question }
	if err := json.Unmarshal([]byte(card.Args), &args); err != nil || len(args.Questions) != 2 {
		t.Fatalf("args %s: %v", card.Args, err)
	}
	if args.Questions[0].Question != "Pick an option" || strings.Join(args.Questions[0].Options, ",") != "A,B,C" || len(args.Questions[1].Options) != 0 {
		t.Fatalf("questions %+v", args.Questions)
	}
	if card.Result != `{"status":"pending"}` {
		t.Fatalf("result %s", card.Result)
	}
	// Ordinary messages, and async ones without questions, are not question cards.
	for _, doc := range []string{
		`{"type":"agentMessage","id":"m","delivery":null,"text":"hi","questions":null}`,
		`{"type":"agentMessage","id":"m","delivery":"async","text":"fyi","questions":null}`,
		`{"type":"agentMessage","id":"m","delivery":"async","text":"fyi","questions":[]}`,
	} {
		if _, ok := QuestionCard(item(t, doc)); ok {
			t.Fatalf("%s took for a question", doc)
		}
	}
}

func TestQuestionIDsMatchWhatCodexItselfWrites(t *testing.T) {
	if got := QuestionID("audience-question", 0); got != `["request_user_input_async","audience-question",0]` {
		t.Fatalf("id %s", got)
	}
	call, index, ok := ParseQuestionID(`["request_user_input_async","call_9",3]`)
	if !ok || call != "call_9" || index != 3 {
		t.Fatalf("parsed %q %d %v", call, index, ok)
	}
	if _, _, ok := ParseQuestionID("not json"); ok {
		t.Fatal("accepted noise")
	}
}

func TestTheReplyIsTheEnvelopeCodexReads(t *testing.T) {
	qs := []harness.Question{{Question: "Pick an option", Options: []string{"A", "B"}}, {Question: "Anything <else> & more?"}}
	as := []harness.Answer{{Question: "Pick an option", Answer: "B"}, {Question: "Anything <else> & more?", Answer: "no thanks"}}
	message, display := adapter.QuestionReply("call_5b86", qs, as)
	if !strings.HasPrefix(message, "<send_user_message_question_reply>\n[") || !strings.HasSuffix(message, "]\n</send_user_message_question_reply>") {
		t.Fatalf("envelope %q", message)
	}
	if !strings.Contains(message, "Anything <else> & more?") {
		t.Fatalf("html was escaped: %q", message)
	}
	replies, ok := ParseReply(message)
	if !ok || len(replies) != 2 || replies[0].Answer != "B" || replies[0].QuestionItemID != `["request_user_input_async","call_5b86",0]` || replies[1].QuestionItemID != `["request_user_input_async","call_5b86",1]` {
		t.Fatalf("replies %+v ok=%v", replies, ok)
	}
	if display != "> Pick an option\n\nB\n\n> Anything <else> & more?\n\nno thanks" {
		t.Fatalf("display %q", display)
	}
}

func TestALongMultilineQuestionIsShortenedOnOneLineInTheReply(t *testing.T) {
	long := "first line\n" + strings.Repeat("x", 600)
	message, _ := adapter.QuestionReply("c", []harness.Question{{Question: long}}, []harness.Answer{{Question: long, Answer: "ok"}})
	replies, ok := ParseReply(message)
	if !ok || strings.Contains(replies[0].Question, "\n") || len(replies[0].Question) > 512 {
		t.Fatalf("question not bounded: %q", replies[0].Question)
	}
}

func msg(role transcript.Role, content string) transcript.Message {
	return transcript.Message{Role: role, Content: content}
}

func questionRows(callID string) []transcript.Message {
	name := harness.CardQuestion
	id := callID
	return []transcript.Message{
		{Role: transcript.RoleAssistant, ToolCalls: &[]transcript.ToolCall{{ID: callID, Name: name, Arguments: `{"questions":[{"question":"Pick","options":["A","B"]}]}`}}},
		{Role: transcript.RoleTool, ToolCallID: &id, Name: &name, Content: `{"status":"pending"}`},
	}
}

func TestALaterReplySettlesTheQuestionAcrossTurnsAndRestarts(t *testing.T) {
	reply, display := adapter.QuestionReply("c1", []harness.Question{{Question: "Pick"}}, []harness.Answer{{Question: "Pick", Answer: "B"}})
	msgs := append([]transcript.Message{msg(transcript.RoleUser, "go")}, questionRows("c1")...)
	msgs = append(msgs, msg(transcript.RoleAssistant, "waiting"), msg(transcript.RoleUser, reply))
	got := adapter.Annotate(msgs)
	if !strings.Contains(got[2].Content, `"answered"`) || !strings.Contains(got[2].Content, `"B"`) {
		t.Fatalf("question still pending: %s", got[2].Content)
	}
	if got[len(got)-1].Display != display {
		t.Fatalf("the reply row shows %q, want %q", got[len(got)-1].Display, display)
	}
	if !strings.Contains(msgs[2].Content, "pending") {
		t.Fatal("the stored rows were changed")
	}
}

func TestAQuestionNobodyAnsweredStaysPending(t *testing.T) {
	msgs := append([]transcript.Message{msg(transcript.RoleUser, "go")}, questionRows("c1")...)
	msgs = append(msgs, msg(transcript.RoleUser, "never mind, something else"))
	if got := adapter.Annotate(msgs); !strings.Contains(got[2].Content, "pending") {
		t.Fatalf("settled by an unrelated message: %s", got[2].Content)
	}
	// A reply to another question does not settle this one.
	other, _ := adapter.QuestionReply("c9", []harness.Question{{Question: "x"}}, []harness.Answer{{Question: "x", Answer: "y"}})
	msgs = append(msgs, msg(transcript.RoleUser, other))
	if got := adapter.Annotate(msgs); !strings.Contains(got[2].Content, "pending") {
		t.Fatalf("settled by another question's reply: %s", got[2].Content)
	}
}

func TestBackgroundCommandCardsKeepTheirArgumentsAndOutput(t *testing.T) {
	running := item(t, `{"type":"commandExecution","id":"e1","status":"inProgress","processId":"71863","command":"/bin/zsh -lc 'sleep 25'","cwd":"/w","source":"unifiedExecStartup","commandActions":[{"type":"unknown","command":"sleep 25"}]}`)
	if got := BackgroundArgs(Card(running).Args); !strings.Contains(got, `"background":true`) || !strings.Contains(got, "sleep 25") {
		t.Fatalf("args %s", got)
	}
	if got := RunningResult("so far\n"); got != `{"output":"so far\n","status":"running"}` {
		t.Fatalf("running result %s", got)
	}
}

func TestACommandThatFinishedLaterGetsItsRealOutcome(t *testing.T) {
	done := item(t, `{"type":"commandExecution","id":"e1","status":"completed","processId":"71863","exitCode":0,"aggregatedOutput":"bgdone\n","command":"/bin/zsh -lc 'sleep 25 && echo bgdone'"}`)
	result, failed := Outcome(done, "")
	if failed || !strings.Contains(result, `"exit_code":0`) || !strings.Contains(result, "bgdone") || !strings.Contains(result, `"completed"`) {
		t.Fatalf("result %s failed=%v", result, failed)
	}
	failedItem := item(t, `{"type":"commandExecution","id":"e1","status":"failed","exitCode":2,"aggregatedOutput":"boom"}`)
	if _, failed := Outcome(failedItem, ""); !failed {
		t.Fatal("a non-zero exit is a failure")
	}
	// A command still running when asked is unknown, never failed.
	still := item(t, `{"type":"commandExecution","id":"e1","status":"inProgress"}`)
	result, failed = Outcome(still, "")
	if failed || !strings.Contains(result, `"unknown"`) {
		t.Fatalf("inProgress outcome %s failed=%v", result, failed)
	}
}

func TestNewerItemTypesGetCards(t *testing.T) {
	cases := []struct{ doc, name string }{
		{`{"type":"imageView","id":"i1","path":"/tmp/a.png"}`, "view_image"},
		{`{"type":"imageGeneration","id":"i2","status":"completed","revisedPrompt":"a cat","savedPath":"/tmp/cat.png","failure":null}`, "image_generation"},
		{`{"type":"subAgentActivity","id":"s1","kind":"started","agentPath":"root/worker","agentThreadId":"t2","model":"gpt-6-luna"}`, "codex_subagent"},
		{`{"type":"webSearch","id":"w1","query":"go generics","action":{"type":"search","query":"go generics"},"results":null}`, "web_search"},
	}
	for _, c := range cases {
		card := Card(item(t, c.doc))
		if card == nil || card.Name != c.name {
			t.Fatalf("%s -> %+v, want %s", c.doc, card, c.name)
		}
	}
	for _, doc := range []string{`{"type":"sleep","id":"z","durationMs":5}`, `{"type":"functionCallOutput","id":"f"}`} {
		if Card(item(t, doc)) != nil && !strings.HasPrefix(Card(item(t, doc)).Name, "codex_") {
			t.Fatalf("%s must not take a built-in card", doc)
		}
	}
	res, failed := Outcome(item(t, cases[1].doc), "")
	if failed || !strings.Contains(res, "/tmp/cat.png") {
		t.Fatalf("image generation result %s", res)
	}
}

func TestDisplayCommandDropsTheShellWrapper(t *testing.T) {
	got := DisplayCommand(item(t, `{"type":"commandExecution","id":"e","command":"/bin/zsh -lc 'ls -la'","commandActions":[]}`))
	if got != "ls -la" {
		t.Fatalf("got %q", got)
	}
}
