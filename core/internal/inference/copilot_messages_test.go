package inference

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/config"
)

type captured struct {
	header http.Header
	body   map[string]any
}

func captureServer(t *testing.T, contentType, reply string) (*httptest.Server, *[]captured) {
	t.Helper()
	var seen []captured
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body := map[string]any{}
		_ = json.Unmarshal(raw, &body)
		seen = append(seen, captured{r.Header.Clone(), body})
		w.Header().Set("Content-Type", contentType)
		fmt.Fprint(w, reply)
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

var (
	adaptiveMeta = config.ModelMeta{Protocol: protocolAnthropic, AdaptiveThinking: true, MaxOutput: 64000, ThinkingKnown: true, ThinkingLevels: []string{"low", "medium", "high"}}
	budgetMeta   = config.ModelMeta{Protocol: protocolAnthropic, MinThinkingBudget: 1024, MaxThinkingBudget: 32000, MaxOutput: 64000}
)

func messagesProvider(t *testing.T, srv *httptest.Server, model string, meta config.ModelMeta) *OpenAIProvider {
	t.Helper()
	p, err := BuildProviderWithMeta(config.ApiTypeCopilot, "github-copilot", model, srv.URL, "tok", meta)
	if err != nil {
		t.Fatal(err)
	}
	return p.(*OpenAIProvider)
}

func anthropicEvents(events ...string) string {
	var b strings.Builder
	for _, e := range events {
		fmt.Fprintf(&b, "event: x\ndata: %s\n\n", e)
	}
	return b.String()
}

const (
	evStart = `{"type":"message_start","message":{"usage":{"input_tokens":10,"cache_creation_input_tokens":5,"cache_read_input_tokens":7}}}`
	evText  = `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`
	evHi    = `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`
	evStop  = `{"type":"message_stop"}`
)

var simpleReply = anthropicEvents(evStart, evText, evHi, `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}`, evStop)

func asList(v any) []any         { l, _ := v.([]any); return l }
func asMap(v any) map[string]any { m, _ := v.(map[string]any); return m }

func cacheMarked(v any) bool { return asMap(asMap(v)["cache_control"])["type"] == "ephemeral" }

func TestCopilotMessagesBodyMatchesVSCode(t *testing.T) {
	t.Setenv("PALADIN_DATA_DIR", t.TempDir())
	srv, seen := captureServer(t, "text/event-stream", simpleReply)
	p := messagesProvider(t, srv, "claude-sonnet-5.5", adaptiveMeta)
	eff, err := WithEffort(p, "high")
	if err != nil {
		t.Fatalf("effort must be settable on the Messages path: %v", err)
	}
	tx := make(chan Chunk, 64)
	done := make(chan struct{})
	tools := []ToolDefinition{{Name: "read", Description: "r", Parameters: map[string]any{"type": "object"}}, {Name: "shell", Description: "s", Parameters: map[string]any{"type": "object"}}}
	if err := eff.StreamTools(context.Background(), "SYS", userTurn("hi"), tools, "s", tx, done); err != nil {
		t.Fatal(err)
	}
	c := (*seen)[0]
	b := c.body
	if b["model"] != "claude-sonnet-5.5" || b["stream"] != true || b["max_tokens"] != float64(64000) {
		t.Fatalf("body %v", b)
	}
	sys := asList(b["system"])
	if len(sys) != 1 || asMap(sys[0])["text"] != "SYS" || !cacheMarked(sys[0]) {
		t.Fatalf("system must be one cached text block: %v", b["system"])
	}
	tl := asList(b["tools"])
	if len(tl) != 2 || cacheMarked(tl[0]) || !cacheMarked(tl[1]) {
		t.Fatalf("only the last tool carries the marker: %v", tl)
	}
	if th := asMap(b["thinking"]); th["type"] != "adaptive" || th["display"] != "summarized" {
		t.Fatalf("thinking %v", b["thinking"])
	}
	if oc := asMap(b["output_config"]); oc["effort"] != "high" {
		t.Fatalf("output_config %v", b["output_config"])
	}
	msgs := asList(b["messages"])
	if len(msgs) != 1 || !cacheMarked(asList(asMap(msgs[0])["content"])[0]) {
		t.Fatalf("last message block must be cached: %v", msgs)
	}
	if c.header.Get("Anthropic-Beta") != "" {
		t.Fatalf("adaptive models need no interleaved-thinking beta: %q", c.header.Get("Anthropic-Beta"))
	}
	if c.header.Get("Openai-Intent") != "conversation-agent" || c.header.Get("X-Request-Id") == "" || c.header.Get("Authorization") != "Bearer tok" {
		t.Fatalf("headers %v", c.header)
	}
}

func TestCopilotMessagesBudgetModeThinking(t *testing.T) {
	t.Setenv("PALADIN_DATA_DIR", t.TempDir())
	srv, seen := captureServer(t, "text/event-stream", simpleReply)
	runStream(t, messagesProvider(t, srv, "claude-haiku-4.5", budgetMeta), userTurn("hi"))
	c := (*seen)[0]
	th := asMap(c.body["thinking"])
	if th["type"] != "enabled" || th["budget_tokens"] != float64(16000) {
		t.Fatalf("thinking %v", c.body["thinking"])
	}
	if _, has := c.body["output_config"]; has {
		t.Fatalf("effort sent to a model that declares none: %v", c.body["output_config"])
	}
	if !strings.Contains(c.header.Get("Anthropic-Beta"), "interleaved-thinking-2025-05-14") {
		t.Fatalf("beta header %q", c.header.Get("Anthropic-Beta"))
	}
}

func TestCopilotMessagesNoThinkingWithoutCapability(t *testing.T) {
	t.Setenv("PALADIN_DATA_DIR", t.TempDir())
	srv, seen := captureServer(t, "text/event-stream", simpleReply)
	runStream(t, messagesProvider(t, srv, "claude-old", config.ModelMeta{Protocol: protocolAnthropic, MaxOutput: 8192}), userTurn("hi"))
	if _, has := (*seen)[0].body["thinking"]; has {
		t.Fatalf("thinking sent without capability: %v", (*seen)[0].body["thinking"])
	}
}

func markers(body map[string]any) (count int, where []string) {
	for i, tool := range asList(body["tools"]) {
		if cacheMarked(tool) {
			count++
			where = append(where, fmt.Sprintf("tool%d", i))
		}
	}
	for i, blk := range asList(body["system"]) {
		if cacheMarked(blk) {
			count++
			where = append(where, fmt.Sprintf("system%d", i))
		}
	}
	for i, m := range asList(body["messages"]) {
		for j, blk := range asList(asMap(m)["content"]) {
			if cacheMarked(blk) {
				count++
				where = append(where, fmt.Sprintf("msg%d.%d(%v)", i, j, asMap(blk)["type"]))
			}
		}
	}
	return
}

func TestCopilotMessagesCacheMarkersStayWithinFourAndRollForward(t *testing.T) {
	t.Setenv("PALADIN_DATA_DIR", t.TempDir())
	srv, seen := captureServer(t, "text/event-stream", simpleReply)
	p := messagesProvider(t, srv, "claude-sonnet-5.5", adaptiveMeta)
	th := &Thinking{Provider: "github-copilot", Model: "claude-sonnet-5.5", Format: thinkingMessages,
		Raw: json.RawMessage(`[{"type":"thinking","thinking":"t","signature":"S"}]`)}
	id1, id2, name := "c1", "c2", "read"
	history := []LlmMessage{
		{Role: "user", Content: "go"},
		{Role: "assistant", Thinking: th, ToolCalls: []ToolCall{{ID: id1, Name: name, Arguments: "{}"}}},
		{Role: "tool", Content: "r1", ToolCallID: &id1, Name: &name},
		{Role: "assistant", Thinking: th, ToolCalls: []ToolCall{{ID: id2, Name: name, Arguments: "{}"}}},
		{Role: "tool", Content: "r2", ToolCallID: &id2, Name: &name},
	}
	tools := []ToolDefinition{{Name: "read", Description: "r", Parameters: map[string]any{"type": "object"}}}
	tx := make(chan Chunk, 64)
	if err := p.StreamTools(context.Background(), "SYS", history, tools, "s", tx, make(chan struct{})); err != nil {
		t.Fatal(err)
	}
	count, where := markers((*seen)[0].body)
	if count != 4 {
		t.Fatalf("markers %d at %v, want tool + system + the last two messages", count, where)
	}
	for _, w := range where {
		if strings.Contains(w, "(thinking)") || strings.Contains(w, "(redacted_thinking)") {
			t.Fatalf("a thinking block carries a marker: %v", where)
		}
	}
	msgs := asList((*seen)[0].body["messages"])
	last := asMap(msgs[len(msgs)-1])
	prev := asMap(msgs[len(msgs)-2])
	if !cacheMarked(asList(last["content"])[len(asList(last["content"]))-1]) {
		t.Fatalf("newest message not marked: %v", last)
	}
	// The older anchor sits on the last non-thinking block of the second-newest message.
	pc := asList(prev["content"])
	if !cacheMarked(pc[len(pc)-1]) || asMap(pc[len(pc)-1])["type"] != "tool_use" {
		t.Fatalf("second anchor misplaced: %v", prev)
	}
}

func thinkingStream() string {
	return anthropicEvents(evStart,
		`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Plan "}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"it"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"SIG"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"redacted_thinking","data":"RED"}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"toolu_1","name":"read"}}`,
		`{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{}"}}`,
		`{"type":"content_block_stop","index":2}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":9}}`, evStop)
}

func TestCopilotMessagesCapturesSignedThinking(t *testing.T) {
	t.Setenv("PALADIN_DATA_DIR", t.TempDir())
	srv, _ := captureServer(t, "text/event-stream", thinkingStream())
	got := runStream(t, messagesProvider(t, srv, "claude-sonnet-5.5", adaptiveMeta), userTurn("hi"))
	if got.reasoning != "Plan it" {
		t.Fatalf("live reasoning %q", got.reasoning)
	}
	th := got.thinking
	if th == nil || th.Format != thinkingMessages || th.Provider != "github-copilot" || th.Model != "claude-sonnet-5.5" {
		t.Fatalf("thinking %+v", th)
	}
	var blocks []map[string]any
	if err := json.Unmarshal(th.Raw, &blocks); err != nil || len(blocks) != 2 ||
		blocks[0]["type"] != "thinking" || blocks[0]["thinking"] != "Plan it" || blocks[0]["signature"] != "SIG" ||
		blocks[1]["type"] != "redacted_thinking" || blocks[1]["data"] != "RED" {
		t.Fatalf("blocks %s (%v)", th.Raw, err)
	}
	if len(got.calls) != 1 || got.calls[0].ID != "toolu_1" {
		t.Fatalf("calls %+v", got.calls)
	}
}

func TestCopilotMessagesUnsignedThinkingIsNotKept(t *testing.T) {
	t.Setenv("PALADIN_DATA_DIR", t.TempDir())
	srv, _ := captureServer(t, "text/event-stream", anthropicEvents(evStart,
		`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"no signature"}}`,
		`{"type":"content_block_stop","index":0}`, evText, evHi, evStop))
	got := runStream(t, messagesProvider(t, srv, "claude-sonnet-5.5", adaptiveMeta), userTurn("hi"))
	if got.thinking != nil && len(got.thinking.Raw) != 0 {
		t.Fatalf("unsigned block kept: %s", got.thinking.Raw)
	}
}

func signedThinking(model string) *Thinking {
	return &Thinking{Text: "t", Provider: "github-copilot", Model: model, Format: thinkingMessages,
		Raw: json.RawMessage(`[{"type":"thinking","thinking":"deep thought","signature":"SIG"},{"type":"redacted_thinking","data":"RED"}]`)}
}

func firstAssistantContent(t *testing.T, body map[string]any) []any {
	t.Helper()
	for _, m := range asList(body["messages"]) {
		if asMap(m)["role"] == "assistant" {
			return asList(asMap(m)["content"])
		}
	}
	t.Fatalf("no assistant message: %v", body["messages"])
	return nil
}

func TestCopilotMessagesReplaysThinkingBeforeToolUse(t *testing.T) {
	t.Setenv("PALADIN_DATA_DIR", t.TempDir())
	srv, seen := captureServer(t, "text/event-stream", simpleReply)
	runStream(t, messagesProvider(t, srv, "claude-sonnet-5.5", adaptiveMeta), replayHistory(signedThinking("claude-sonnet-5.5")))
	c := firstAssistantContent(t, (*seen)[0].body)
	if len(c) != 3 || asMap(c[0])["type"] != "thinking" || asMap(c[0])["signature"] != "SIG" || asMap(c[0])["thinking"] != "deep thought" ||
		asMap(c[1])["type"] != "redacted_thinking" || asMap(c[1])["data"] != "RED" || asMap(c[2])["type"] != "tool_use" {
		t.Fatalf("assistant content %v", c)
	}
	if cacheMarked(c[0]) || cacheMarked(c[1]) {
		t.Fatal("thinking blocks must never carry a cache marker")
	}
}

func TestCopilotMessagesDropsAnotherModelsThinking(t *testing.T) {
	t.Setenv("PALADIN_DATA_DIR", t.TempDir())
	srv, seen := captureServer(t, "text/event-stream", simpleReply)
	runStream(t, messagesProvider(t, srv, "claude-opus-5.5", adaptiveMeta), replayHistory(signedThinking("claude-sonnet-5.5")))
	for _, blk := range firstAssistantContent(t, (*seen)[0].body) {
		if ty := asMap(blk)["type"]; ty == "thinking" || ty == "redacted_thinking" {
			t.Fatalf("foreign thinking sent: %v", blk)
		}
	}
}

func historicalThinkingBlocks(t *testing.T, model string, meta config.ModelMeta) int {
	t.Helper()
	srv, seen := captureServer(t, "text/event-stream", simpleReply)
	runStream(t, messagesProvider(t, srv, model, meta), twoTurnHistory(signedThinking(model), nil))
	n := 0
	for _, m := range asList((*seen)[0].body["messages"]) {
		for _, blk := range asList(asMap(m)["content"]) {
			if ty := asMap(blk)["type"]; ty == "thinking" || ty == "redacted_thinking" {
				n++
			}
		}
	}
	return n
}

func TestCopilotMessagesReplaysEarlierPromptsOnlyForAdaptiveModels(t *testing.T) {
	t.Setenv("PALADIN_DATA_DIR", t.TempDir())
	if n := historicalThinkingBlocks(t, "claude-sonnet-5.5", adaptiveMeta); n != 2 {
		t.Fatalf("adaptive model replayed %d blocks from the earlier prompt, want 2", n)
	}
	// Budget-mode models answer HTTP 400 to replayed history thinking (vscode#318076).
	if n := historicalThinkingBlocks(t, "claude-haiku-4.5", budgetMeta); n != 0 {
		t.Fatalf("budget-mode model was sent %d historical blocks", n)
	}
}

func TestCopilotMessagesBudgetModelStillReplaysWithinThePrompt(t *testing.T) {
	t.Setenv("PALADIN_DATA_DIR", t.TempDir())
	srv, seen := captureServer(t, "text/event-stream", simpleReply)
	runStream(t, messagesProvider(t, srv, "claude-haiku-4.5", budgetMeta), replayHistory(signedThinking("claude-haiku-4.5")))
	if c := firstAssistantContent(t, (*seen)[0].body); len(c) < 1 || asMap(c[0])["type"] != "thinking" {
		t.Fatalf("current prompt thinking dropped: %v", c)
	}
}

func TestCopilotMessagesTurnsThinkingOffWhenAToolRoundHasNone(t *testing.T) {
	t.Setenv("PALADIN_DATA_DIR", t.TempDir())
	srv, seen := captureServer(t, "text/event-stream", simpleReply)
	p := messagesProvider(t, srv, "claude-sonnet-5.5", adaptiveMeta)
	// A tool round that began without a thinking block cannot continue under thinking.
	runStream(t, p, replayHistory(nil))
	if _, has := (*seen)[0].body["thinking"]; has {
		t.Fatalf("thinking on over a tool round without thinking: %v", (*seen)[0].body["thinking"])
	}
	if _, has := (*seen)[0].body["output_config"]; has {
		t.Fatal("effort sent with thinking off")
	}
	runStream(t, p, replayHistory(signedThinking("claude-sonnet-5.5")))
	if _, has := (*seen)[1].body["thinking"]; !has {
		t.Fatal("thinking should stay on when the round carries it")
	}
	runStream(t, p, userTurn("a fresh prompt"))
	if _, has := (*seen)[2].body["thinking"]; !has {
		t.Fatal("a fresh prompt should think")
	}
}

func TestCopilotMessagesSanitizesToolIDsAndKeepsPairs(t *testing.T) {
	t.Setenv("PALADIN_DATA_DIR", t.TempDir())
	srv, seen := captureServer(t, "text/event-stream", simpleReply)
	a, b, name := "call|1", "call:1", "read"
	runStream(t, messagesProvider(t, srv, "claude-sonnet-5.5", adaptiveMeta), []LlmMessage{
		{Role: "user", Content: "go"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: a, Name: name, Arguments: "{}"}, {ID: b, Name: name, Arguments: "{}"}}},
		{Role: "tool", Content: "ra", ToolCallID: &a, Name: &name},
		{Role: "tool", Content: "rb", ToolCallID: &b, Name: &name},
	})
	msgs := asList((*seen)[0].body["messages"])
	var uses, results []string
	for _, blk := range asList(asMap(msgs[1])["content"]) {
		if asMap(blk)["type"] == "tool_use" {
			uses = append(uses, asMap(blk)["id"].(string))
		}
	}
	for _, blk := range asList(asMap(msgs[2])["content"]) {
		if asMap(blk)["type"] == "tool_result" {
			results = append(results, asMap(blk)["tool_use_id"].(string))
		}
	}
	if strings.Join(uses, ",") != "call_1,call_1_1" || strings.Join(results, ",") != "call_1,call_1_1" {
		t.Fatalf("uses %v results %v", uses, results)
	}
}

func TestCopilotMessagesNeverEndsOnAnAssistantMessage(t *testing.T) {
	t.Setenv("PALADIN_DATA_DIR", t.TempDir())
	srv, seen := captureServer(t, "text/event-stream", simpleReply)
	runStream(t, messagesProvider(t, srv, "claude-sonnet-5.5", adaptiveMeta), []LlmMessage{
		{Role: "user", Content: "q"}, {Role: "assistant", Content: "partial answer"},
	})
	msgs := asList((*seen)[0].body["messages"])
	last := asMap(msgs[len(msgs)-1])
	if last["role"] != "user" || asMap(asList(last["content"])[0])["text"] != "Please continue." {
		t.Fatalf("last message %v", last)
	}
}

func TestCopilotMessagesMergesNeighboursAndSkipsEmptyContent(t *testing.T) {
	t.Setenv("PALADIN_DATA_DIR", t.TempDir())
	srv, seen := captureServer(t, "text/event-stream", simpleReply)
	runStream(t, messagesProvider(t, srv, "claude-sonnet-5.5", adaptiveMeta), []LlmMessage{
		{Role: "user", Content: "one"}, {Role: "user", Content: "   "}, {Role: "user", Content: "two"},
		{Role: "assistant", Content: "  "}, {Role: "user", Content: "three"},
	})
	msgs := asList((*seen)[0].body["messages"])
	if len(msgs) != 1 || len(asList(asMap(msgs[0])["content"])) != 3 {
		t.Fatalf("messages %v", msgs)
	}
}

func TestCopilotMessagesRefusalIsAnError(t *testing.T) {
	t.Setenv("PALADIN_DATA_DIR", t.TempDir())
	srv, _ := captureServer(t, "text/event-stream", anthropicEvents(evStart, `{"type":"message_delta","delta":{"stop_reason":"refusal"},"usage":{"output_tokens":0}}`, evStop))
	tx := make(chan Chunk, 8)
	err := messagesProvider(t, srv, "claude-sonnet-5.5", adaptiveMeta).StreamTools(context.Background(), "", userTurn("hi"), nil, "s", tx, make(chan struct{}))
	if err == nil || !strings.Contains(err.Error(), "refus") {
		t.Fatalf("err %v", err)
	}
}

func TestCopilotMessagesUsageCountsCacheWrites(t *testing.T) {
	t.Setenv("PALADIN_DATA_DIR", t.TempDir())
	srv, _ := captureServer(t, "text/event-stream", simpleReply)
	p := messagesProvider(t, srv, "claude-sonnet-5.5", adaptiveMeta)
	tx := make(chan Chunk, 32)
	if err := p.StreamTools(context.Background(), "", userTurn("hi"), nil, "s", tx, make(chan struct{})); err != nil {
		t.Fatal(err)
	}
	close(tx)
	var u *Usage
	for c := range tx {
		if c.Usage != nil {
			u = c.Usage
		}
	}
	if u == nil || u.PromptTokens != 22 || u.CacheReadTokens != 7 || u.CacheWriteTokens != 5 || u.CompletionTokens != 3 {
		t.Fatalf("usage %+v", u)
	}
}

func TestCopilotMessagesBufferedCallCapturesThinkingAndCapsOutput(t *testing.T) {
	t.Setenv("PALADIN_DATA_DIR", t.TempDir())
	srv, seen := captureServer(t, "application/json", `{"content":[{"type":"thinking","thinking":"why","signature":"S1"},{"type":"text","text":"ok"}],"usage":{"input_tokens":4,"output_tokens":2}}`)
	res, err := messagesProvider(t, srv, "claude-sonnet-5.5", adaptiveMeta).CompleteTools(context.Background(), "", userTurn("hi"), nil, "s")
	if err != nil || res.Content != "ok" || res.Reasoning != "why" || res.Thinking == nil || res.Thinking.Format != thinkingMessages {
		t.Fatalf("res %+v err %v", res, err)
	}
	if mt := (*seen)[0].body["max_tokens"]; mt != float64(16384) {
		t.Fatalf("a non-streamed call must stay under the long-request cap: %v", mt)
	}
}

func TestCopilotMessagesEffortLevelsAreOffered(t *testing.T) {
	levels := ModelEffortLevels(config.ApiTypeCopilot, "", "claude-sonnet-5.5", adaptiveMeta)
	if strings.Join(levels, ",") != "low,medium,high" {
		t.Fatalf("levels %v", levels)
	}
}

// stripCacheMarkers re-encodes a request with its cache_control markers removed,
// so two requests can be compared on what the model is actually sent.
func stripCacheMarkers(t *testing.T, v any) any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var tree any
	if err := json.Unmarshal(raw, &tree); err != nil {
		t.Fatal(err)
	}
	var strip func(any)
	strip = func(n any) {
		switch x := n.(type) {
		case map[string]any:
			delete(x, "cache_control")
			for _, c := range x {
				strip(c)
			}
		case []any:
			for _, c := range x {
				strip(c)
			}
		}
	}
	strip(tree)
	return tree
}

func thoughtful(model, text string) *Thinking {
	raw, _ := json.Marshal([]map[string]any{{"type": "thinking", "thinking": text, "signature": "SIG-" + text}})
	return &Thinking{Format: thinkingMessages, Provider: "github-copilot", Model: model, Raw: raw}
}

// Prompt caching only pays when each request begins with exactly what the last one
// sent. This builds a conversation round by round, across two user prompts, and
// requires that every request carries the previous one's tools, system prompt,
// thinking setting and messages unchanged.
func TestCopilotMessagesEachRequestExtendsThePreviousOne(t *testing.T) {
	t.Setenv("PALADIN_DATA_DIR", t.TempDir())
	srv, _ := captureServer(t, "text/event-stream", simpleReply)
	const model = "claude-sonnet-5.5"
	p := messagesProvider(t, srv, model, adaptiveMeta)
	tools := []ToolDefinition{
		{Name: "read", Description: "r", Parameters: map[string]any{"type": "object"}},
		{Name: "shell", Description: "s", Parameters: map[string]any{"type": "object"}},
	}
	call := func(id string) ToolCall { return ToolCall{ID: id, Name: "read", Arguments: `{"path":"a.go"}`} }
	result := func(id, out string) LlmMessage {
		return LlmMessage{Role: "tool", ToolCallID: &id, Name: new(string), Content: out}
	}
	steps := []LlmMessage{
		{Role: "user", Content: "fix the bug"},
		{Role: "assistant", Thinking: thoughtful(model, "one"), ToolCalls: []ToolCall{call("t1"), call("t2")}},
		result("t1", "file one"), result("t2", "file two"),
		{Role: "assistant", Thinking: thoughtful(model, "two"), Content: "reading more", ToolCalls: []ToolCall{call("t3")}},
		result("t3", "file three"),
		{Role: "assistant", Thinking: thoughtful(model, "three"), Content: "done"},
		{Role: "user", Content: "now add a test"},
		{Role: "assistant", Thinking: thoughtful(model, "four"), ToolCalls: []ToolCall{call("t4")}},
		result("t4", "file four"),
	}
	// The points where the agent loop asks the model for its next move.
	asks := []int{1, 4, 6, 8, 9}
	var prev anthropicRequest
	for n, upto := range asks {
		req := p.copilotMessagesRequest("SYSTEM PROMPT", steps[:upto], tools)
		if n > 0 {
			if got, want := stripCacheMarkers(t, req.Tools), stripCacheMarkers(t, prev.Tools); !reflect.DeepEqual(got, want) {
				t.Fatalf("ask %d: tools changed between requests", n)
			}
			if got, want := stripCacheMarkers(t, req.System), stripCacheMarkers(t, prev.System); !reflect.DeepEqual(got, want) {
				t.Fatalf("ask %d: system prompt changed between requests", n)
			}
			if got, want := stripCacheMarkers(t, req.Thinking), stripCacheMarkers(t, prev.Thinking); !reflect.DeepEqual(got, want) {
				t.Fatalf("ask %d: the thinking setting changed between requests (%v -> %v), which discards the cached messages", n, want, got)
			}
			old := stripCacheMarkers(t, prev.Messages).([]any)
			now := stripCacheMarkers(t, req.Messages).([]any)
			if len(now) < len(old) || !reflect.DeepEqual(now[:len(old)], old) {
				t.Fatalf("ask %d: the request does not begin with the previous one\nprevious: %v\nnow:      %v", n, old, now)
			}
		}
		prev = req
	}
}
