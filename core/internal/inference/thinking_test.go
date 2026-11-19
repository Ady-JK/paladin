package inference

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/config"
)

// recordServer answers every request with reply and keeps the last body.
func recordServer(t *testing.T, contentType, reply string) (*httptest.Server, *map[string]any) {
	t.Helper()
	body := map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = map[string]any{}
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", contentType)
		fmt.Fprint(w, reply)
	}))
	t.Cleanup(srv.Close)
	return srv, &body
}

func copilotProviderAt(t *testing.T, srv *httptest.Server, model string) *OpenAIProvider {
	t.Helper()
	p, err := BuildProvider(config.ApiTypeCopilot, "github-copilot", model, srv.URL, "tok")
	if err != nil {
		t.Fatal(err)
	}
	return p.(*OpenAIProvider)
}

type streamed struct {
	text, reasoning string
	thinking        *Thinking
	calls           []ToolCall
}

func runStream(t *testing.T, p Provider, messages []LlmMessage) streamed {
	t.Helper()
	return runStreamCtx(t, context.Background(), p, messages)
}

func runStreamCtx(t *testing.T, ctx context.Context, p Provider, messages []LlmMessage) streamed {
	t.Helper()
	tx := make(chan Chunk, 256)
	done := make(chan struct{})
	if err := p.StreamTools(ctx, "", messages, nil, "s", tx, done); err != nil {
		t.Fatal(err)
	}
	close(done)
	close(tx)
	var out streamed
	for c := range tx {
		out.text += c.Text
		out.reasoning += c.Reasoning
		if c.Thinking != nil {
			out.thinking = c.Thinking
		}
		if len(c.ToolCalls) > 0 {
			out.calls = c.ToolCalls
		}
	}
	return out
}

func chatSSE(deltas ...string) string {
	var b strings.Builder
	for _, d := range deltas {
		fmt.Fprintf(&b, "data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":%s}]}\n\n", d)
	}
	b.WriteString("data: [DONE]\n\n")
	return b.String()
}

func TestCopilotChatCapturesReasoningTextAndOpaque(t *testing.T) {
	srv, _ := recordServer(t, "text/event-stream", chatSSE(
		`{"role":"assistant","reasoning_text":"first "}`,
		`{"reasoning_text":"second"}`,
		`{"reasoning_opaque":"OPAQUE-1"}`,
		`{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"read","arguments":"{}"}}]}`,
	))
	got := runStream(t, copilotProviderAt(t, srv, "claude-sonnet-5.5"), []LlmMessage{{Role: "user", Content: "hi"}})
	if got.reasoning != "first second" {
		t.Fatalf("live reasoning %q", got.reasoning)
	}
	th := got.thinking
	if th == nil || th.Format != thinkingCopilotChat || th.Opaque != "OPAQUE-1" || th.Provider != "github-copilot" || th.Model != "claude-sonnet-5.5" {
		t.Fatalf("thinking %+v", th)
	}
}

func TestCopilotChatKeepsLatestOpaque(t *testing.T) {
	srv, _ := recordServer(t, "text/event-stream", chatSSE(
		`{"reasoning_opaque":"OLD"}`,
		`{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"read","arguments":"{}"}}]}`,
		`{"reasoning_opaque":"NEW"}`,
	))
	got := runStream(t, copilotProviderAt(t, srv, "claude-sonnet-5.5"), nil)
	if got.thinking == nil || got.thinking.Opaque != "NEW" {
		t.Fatalf("thinking %+v", got.thinking)
	}
}

func TestCopilotChatCompleteCapturesThinking(t *testing.T) {
	srv, _ := recordServer(t, "application/json", `{"id":"1","object":"chat.completion","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"ok","reasoning_text":"why","reasoning_opaque":"OP"}}]}`)
	res, err := copilotProviderAt(t, srv, "claude-sonnet-5.5").CompleteTools(context.Background(), "", []LlmMessage{{Role: "user", Content: "hi"}}, nil, "s")
	if err != nil {
		t.Fatal(err)
	}
	if res.Reasoning != "why" || res.Thinking == nil || res.Thinking.Opaque != "OP" {
		t.Fatalf("res %+v thinking %+v", res, res.Thinking)
	}
}

func assistantOf(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	for _, m := range body["messages"].([]any) {
		if mm := m.(map[string]any); mm["role"] == "assistant" {
			return mm
		}
	}
	t.Fatalf("no assistant message in %v", body["messages"])
	return nil
}

func replayHistory(th *Thinking) []LlmMessage {
	id := "call_1"
	name := "read"
	return []LlmMessage{
		{Role: "user", Content: "go"},
		{Role: "assistant", Content: "", Thinking: th, ToolCalls: []ToolCall{{ID: id, Name: name, Arguments: "{}"}}},
		{Role: "tool", Content: "result", ToolCallID: &id, Name: &name},
	}
}

func TestCopilotChatReplaysNativeThinking(t *testing.T) {
	srv, body := recordServer(t, "text/event-stream", chatSSE(`{"content":"done"}`))
	th := &Thinking{Text: "my plan", Provider: "github-copilot", Model: "claude-sonnet-5.5", Format: thinkingCopilotChat, Opaque: "OPAQUE"}
	runStream(t, copilotProviderAt(t, srv, "claude-sonnet-5.5"), replayHistory(th))
	a := assistantOf(t, *body)
	if a["reasoning_text"] != "my plan" || a["reasoning_opaque"] != "OPAQUE" {
		t.Fatalf("assistant %v", a)
	}
	if strings.Contains(fmt.Sprint(a["content"]), "prior_reasoning") {
		t.Fatalf("native replay must not also inline the text: %v", a["content"])
	}
}

func TestCopilotDropsAnotherModelsReasoning(t *testing.T) {
	srv, body := recordServer(t, "text/event-stream", chatSSE(`{"content":"done"}`))
	th := &Thinking{Text: "my plan", Provider: "github-copilot", Model: "claude-sonnet-5.5", Format: thinkingCopilotChat, Opaque: "OPAQUE"}
	runStream(t, copilotProviderAt(t, srv, "gemini-3.8-flash"), replayHistory(th))
	a := assistantOf(t, *body)
	if _, has := a["reasoning_opaque"]; has || strings.Contains(fmt.Sprint(a["content"]), "my plan") {
		t.Fatalf("another model's reasoning reached Copilot: %v", a)
	}
}

func TestCopilotDropsTextOnlyReasoning(t *testing.T) {
	srv, body := recordServer(t, "text/event-stream", chatSSE(`{"content":"done"}`))
	runStream(t, copilotProviderAt(t, srv, "claude-sonnet-5.5"), replayHistory(&Thinking{Text: "only text"}))
	if a := assistantOf(t, *body); strings.Contains(fmt.Sprint(a["content"]), "only text") {
		t.Fatalf("readable text sent to Copilot: %v", a)
	}
}

func TestOtherProvidersKeepReadableReasoningAcrossAModelSwitch(t *testing.T) {
	srv, body := recordServer(t, "text/event-stream", chatSSE(`{"content":"done"}`))
	th := &Thinking{Text: "my plan", Provider: "litellm", Model: "claude-sonnet-5", Format: thinkingContent}
	runStream(t, NewOpenAIProvider("litellm", "gpt-6", srv.URL, "k"), replayHistory(th))
	a := assistantOf(t, *body)
	if _, has := a["reasoning_content"]; has {
		t.Fatalf("encrypted state crossed models: %v", a)
	}
	if !strings.Contains(fmt.Sprint(a["content"]), "<prior_reasoning>\nmy plan\n</prior_reasoning>") {
		t.Fatalf("readable fallback missing: %v", a["content"])
	}
}

// twoTurnHistory is a finished first prompt, then a second prompt whose tool round is under way.
func twoTurnHistory(old, current *Thinking) []LlmMessage {
	id1, id2, name := "call_old", "call_new", "read"
	return []LlmMessage{
		{Role: "user", Content: "first prompt"},
		{Role: "assistant", Thinking: old, ToolCalls: []ToolCall{{ID: id1, Name: name, Arguments: "{}"}}},
		{Role: "tool", Content: "r1", ToolCallID: &id1, Name: &name},
		{Role: "assistant", Content: "first answer"},
		{Role: "user", Content: "second prompt"},
		{Role: "assistant", Thinking: current, ToolCalls: []ToolCall{{ID: id2, Name: name, Arguments: "{}"}}},
		{Role: "tool", Content: "r2", ToolCallID: &id2, Name: &name},
	}
}

func TestCopilotChatReplaysReasoningOnlyWithinTheCurrentPrompt(t *testing.T) {
	srv, body := recordServer(t, "text/event-stream", chatSSE(`{"content":"done"}`))
	mk := func(op string) *Thinking {
		return &Thinking{Text: op, Provider: "github-copilot", Model: "gemini-3.8-flash", Format: thinkingCopilotChat, Opaque: op}
	}
	runStream(t, copilotProviderAt(t, srv, "gemini-3.8-flash"), twoTurnHistory(mk("OLD"), mk("NEW")))
	var sent []string
	for _, m := range (*body)["messages"].([]any) {
		if mm := m.(map[string]any); mm["role"] == "assistant" {
			if op, ok := mm["reasoning_opaque"].(string); ok {
				sent = append(sent, op)
			}
		}
	}
	if strings.Join(sent, ",") != "NEW" {
		t.Fatalf("replayed %v, want only the current prompt's", sent)
	}
}

func TestCopilotResponsesReplaysReasoningFromEarlierPrompts(t *testing.T) {
	srv, body := recordServer(t, "text/event-stream", responsesDone(`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}`))
	mk := func(id string) *Thinking {
		raw := `[{"type":"reasoning","id":"` + id + `","summary":[],"encrypted_content":"ENC-` + id + `"}]`
		return &Thinking{Provider: "github-copilot", Model: "gpt-5.6-luna", Format: thinkingResponses, Raw: json.RawMessage(raw)}
	}
	runStream(t, copilotProviderAt(t, srv, "gpt-5.6-luna"), twoTurnHistory(mk("rs_old"), mk("rs_new")))
	raw, _ := json.Marshal((*body)["input"])
	if !strings.Contains(string(raw), "ENC-rs_old") || !strings.Contains(string(raw), "ENC-rs_new") {
		t.Fatalf("Responses must replay every round's reasoning: %s", raw)
	}
}

func TestLiteLLMReplaysReasoningFromEarlierPrompts(t *testing.T) {
	srv, body := recordServer(t, "text/event-stream", chatSSE(`{"content":"done"}`))
	mk := func(txt string) *Thinking {
		return &Thinking{Text: txt, Provider: "litellm", Model: "r1", Format: thinkingContent}
	}
	runStream(t, NewOpenAIProvider("litellm", "r1", srv.URL, "k"), twoTurnHistory(mk("old chain"), mk("new chain")))
	n := 0
	for _, m := range (*body)["messages"].([]any) {
		if mm := m.(map[string]any); mm["role"] == "assistant" && mm["reasoning_content"] != nil {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("replayed %d chains, want 2", n)
	}
}

func TestOpencodeIgnoresThinking(t *testing.T) {
	srv, body := recordServer(t, "text/event-stream", chatSSE(`{"content":"done"}`))
	p := NewOpenAIProvider("oc", "some-model", srv.URL, "k")
	p.isOpencode = true
	th := &Thinking{Text: "my plan", Provider: "oc", Model: "some-model", Format: thinkingContent}
	runStream(t, p, replayHistory(th))
	a := assistantOf(t, *body)
	if _, has := a["reasoning_content"]; has || strings.Contains(fmt.Sprint(a["content"]), "prior_reasoning") {
		t.Fatalf("OpenCode request changed: %v", a)
	}
}

func TestLiteLLMThinkingBlocksCaptureAndReplay(t *testing.T) {
	srv, body := recordServer(t, "text/event-stream", chatSSE(
		`{"role":"assistant","reasoning_content":"I am ","thinking_blocks":[{"index":0,"type":"thinking","thinking":"I am "}]}`,
		`{"reasoning_content":"thinking","thinking_blocks":[{"index":0,"type":"thinking","thinking":"thinking"}]}`,
		`{"thinking_blocks":[{"index":0,"type":"thinking","thinking":"","signature":"SIG"}]}`,
		`{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"read","arguments":"{}"}}]}`,
	))
	p := NewOpenAIProvider("litellm", "claude-sonnet-5", srv.URL, "k")
	got := runStream(t, p, []LlmMessage{{Role: "user", Content: "hi"}})
	th := got.thinking
	if th == nil || th.Format != thinkingBlocks || th.Text != "I am thinking" {
		t.Fatalf("thinking %+v", th)
	}
	var blocks []map[string]any
	if err := json.Unmarshal(th.Raw, &blocks); err != nil || len(blocks) != 1 ||
		blocks[0]["thinking"] != "I am thinking" || blocks[0]["signature"] != "SIG" {
		t.Fatalf("blocks %s err %v", th.Raw, err)
	}

	runStream(t, p, replayHistory(th))
	a := assistantOf(t, *body)
	sent, _ := a["thinking_blocks"].([]any)
	if len(sent) != 1 || sent[0].(map[string]any)["signature"] != "SIG" {
		t.Fatalf("assistant %v", a)
	}
	if _, has := a["reasoning_opaque"]; has {
		t.Fatalf("Copilot field leaked to LiteLLM: %v", a)
	}
}

func TestLiteLLMUnsignedBlocksAreNotReplayed(t *testing.T) {
	srv, _ := recordServer(t, "text/event-stream", chatSSE(
		`{"reasoning_content":"x","thinking_blocks":[{"index":0,"type":"thinking","thinking":"x"}]}`,
	))
	got := runStream(t, NewOpenAIProvider("litellm", "claude-sonnet-5", srv.URL, "k"), nil)
	if got.thinking == nil || got.thinking.Format != thinkingContent || len(got.thinking.Raw) != 0 {
		t.Fatalf("thinking %+v", got.thinking)
	}
}

func TestReasoningContentEchoedForPlainEndpoint(t *testing.T) {
	srv, body := recordServer(t, "text/event-stream", chatSSE(`{"content":"done"}`))
	p := NewOpenAIProvider("deepseek", "r1", srv.URL, "k")
	th := &Thinking{Text: "chain", Provider: "deepseek", Model: "r1", Format: thinkingContent}
	runStream(t, p, replayHistory(th))
	if a := assistantOf(t, *body); a["reasoning_content"] != "chain" {
		t.Fatalf("assistant %v", a)
	}
}

const responsesReasoningItem = `{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"plan A"}],"encrypted_content":"ENC","status":"completed"}`

func responsesDone(items ...string) string {
	return "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r\",\"object\":\"response\",\"status\":\"completed\",\"model\":\"gpt-5.6-luna\",\"output\":[" +
		strings.Join(items, ",") + "]}}\n\ndata: [DONE]\n\n"
}

func TestCopilotResponsesCapturesReasoningItems(t *testing.T) {
	srv, body := recordServer(t, "text/event-stream", responsesDone(responsesReasoningItem,
		`{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read","arguments":"{}"}`))
	got := runStream(t, copilotProviderAt(t, srv, "gpt-5.6-luna"), []LlmMessage{{Role: "user", Content: "hi"}})
	th := got.thinking
	if th == nil || th.Format != thinkingResponses || th.Model != "gpt-5.6-luna" {
		t.Fatalf("thinking %+v", th)
	}
	var items []map[string]any
	if json.Unmarshal(th.Raw, &items) != nil || len(items) != 1 || items[0]["encrypted_content"] != "ENC" {
		t.Fatalf("items %s", th.Raw)
	}
	include, _ := (*body)["include"].([]any)
	if len(include) != 1 || include[0] != "reasoning.encrypted_content" || (*body)["store"] != false {
		t.Fatalf("request must ask for encrypted reasoning without storing: include=%v store=%v", include, (*body)["store"])
	}
}

func TestCopilotResponsesReplaysReasoningBeforeCall(t *testing.T) {
	srv, body := recordServer(t, "text/event-stream", responsesDone(`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}`))
	th := &Thinking{Text: "plan A", Provider: "github-copilot", Model: "gpt-5.6-luna", Format: thinkingResponses, Raw: json.RawMessage("[" + responsesReasoningItem + "]")}
	runStream(t, copilotProviderAt(t, srv, "gpt-5.6-luna"), replayHistory(th))
	var types []string
	for _, it := range (*body)["input"].([]any) {
		m := it.(map[string]any)
		ty, _ := m["type"].(string)
		if ty == "" {
			ty = fmt.Sprint(m["role"])
		}
		types = append(types, ty)
		if ty == "reasoning" && m["encrypted_content"] != "ENC" {
			t.Fatalf("reasoning item altered: %v", m)
		}
	}
	if strings.Join(types, ",") != "user,reasoning,function_call,function_call_output" {
		t.Fatalf("input order %v", types)
	}
}

func TestCopilotResponsesDropsAnotherModelsReasoning(t *testing.T) {
	srv, body := recordServer(t, "text/event-stream", responsesDone(`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}`))
	th := &Thinking{Text: "plan A", Provider: "github-copilot", Model: "gpt-5.6-luna", Format: thinkingResponses, Raw: json.RawMessage("[" + responsesReasoningItem + "]")}
	runStream(t, copilotProviderAt(t, srv, "gpt-5.6-sol"), replayHistory(th))
	raw, _ := json.Marshal((*body)["input"])
	if strings.Contains(string(raw), "ENC") || strings.Contains(string(raw), "plan A") {
		t.Fatalf("input %s", raw)
	}
}

// sequenceServer answers request n with replies[n] (the last one repeats)
// and keeps every request body.
func sequenceServer(t *testing.T, contentType string, replies ...string) (*httptest.Server, *[]map[string]any) {
	t.Helper()
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body := map[string]any{}
		_ = json.Unmarshal(raw, &body)
		reply := replies[min(len(bodies), len(replies)-1)]
		bodies = append(bodies, body)
		w.Header().Set("Content-Type", contentType)
		fmt.Fprint(w, reply)
	}))
	t.Cleanup(srv.Close)
	return srv, &bodies
}

const filteredSSE = "data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"finish_reason\":\"content_filter\",\"delta\":{\"role\":\"assistant\"}}]}\n\ndata: [DONE]\n\n"

func foreignHistory() []LlmMessage {
	return replayHistory(&Thinking{Text: strings.Repeat("I am checking each candidate carefully. ", 20),
		Provider: "litellm", Model: "other-model", Format: thinkingContent})
}

func gatewayProvider(model, url string) *OpenAIProvider {
	return NewOpenAIProvider("litellm", model, url, "k")
}

func TestFilteredReadableReasoningIsRetriedWithoutIt(t *testing.T) {
	srv, bodies := sequenceServer(t, "text/event-stream", filteredSSE, chatSSE(`{"content":"160"}`))
	p := gatewayProvider("claude-filter-stream", srv.URL)
	got := runStream(t, p, foreignHistory())
	if got.text != "160" || len(*bodies) != 2 {
		t.Fatalf("text %q after %d requests", got.text, len(*bodies))
	}
	if !strings.Contains(fmt.Sprint(assistantOf(t, (*bodies)[0])["content"]), "prior_reasoning") {
		t.Fatal("first attempt should have carried the readable reasoning")
	}
	if retry := assistantOf(t, (*bodies)[1]); strings.Contains(fmt.Sprint(retry["content"]), "prior_reasoning") {
		t.Fatalf("retry still carries it: %v", retry)
	}
	// The model is remembered as refusing it, so later calls skip the wasted attempt.
	runStream(t, gatewayProvider("claude-filter-stream", srv.URL), foreignHistory())
	if len(*bodies) != 3 || strings.Contains(fmt.Sprint(assistantOf(t, (*bodies)[2])["content"]), "prior_reasoning") {
		t.Fatalf("filtered model was asked again: %d requests", len(*bodies))
	}
}

func TestFilteredReplyWithoutReadableReasoningIsNotRetried(t *testing.T) {
	srv, bodies := sequenceServer(t, "text/event-stream", filteredSSE, chatSSE(`{"content":"never"}`))
	got := runStream(t, gatewayProvider("claude-filter-plain", srv.URL), []LlmMessage{{Role: "user", Content: "hi"}})
	if got.text != "" || len(*bodies) != 1 {
		t.Fatalf("text %q after %d requests", got.text, len(*bodies))
	}
}

func TestFilteredBufferedReadableReasoningIsRetriedWithoutIt(t *testing.T) {
	filtered := `{"id":"1","object":"chat.completion","choices":[{"index":0,"finish_reason":"content_filter","message":{"role":"assistant"}}]}`
	ok := `{"id":"2","object":"chat.completion","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"160"}}]}`
	srv, bodies := sequenceServer(t, "application/json", filtered, ok)
	res, err := gatewayProvider("claude-filter-buffered", srv.URL).CompleteTools(context.Background(), "", foreignHistory(), nil, "s")
	if err != nil || res.Content != "160" || len(*bodies) != 2 {
		t.Fatalf("res %+v err %v after %d requests", res, err, len(*bodies))
	}
}
