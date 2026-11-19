package inference

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/config"
)

func chatMeta() config.ModelMeta {
	return config.ModelMeta{Protocol: protocolChat, MaxOutput: 32000, ThinkingKnown: true, ThinkingLevels: []string{"low", "medium", "high"}}
}

func TestCopilotChatBodyMatchesVSCode(t *testing.T) {
	t.Setenv("PALADIN_DATA_DIR", t.TempDir())
	srv, seen := captureServer(t, "text/event-stream", chatSSE(`{"content":"ok"}`))
	p, err := BuildProviderWithMeta(config.ApiTypeCopilot, "github-copilot", "gemini-3.8-flash", srv.URL, "tok", chatMeta())
	if err != nil {
		t.Fatal(err)
	}
	tx := make(chan Chunk, 16)
	tools := []ToolDefinition{{Name: "read", Description: "r", Parameters: map[string]any{"type": "object"}}}
	if err := p.StreamTools(context.Background(), "SYS", userTurn("hi"), tools, "s", tx, make(chan struct{})); err != nil {
		t.Fatal(err)
	}
	b := (*seen)[0].body
	for key, want := range map[string]any{"temperature": 0.1, "top_p": float64(1), "n": float64(1), "max_tokens": float64(32000), "tool_choice": "validated", "stream": true} {
		if b[key] != want {
			t.Errorf("%s = %v, want %v", key, b[key], want)
		}
	}
}

func TestCopilotChatLeavesToolChoiceAloneForOtherModels(t *testing.T) {
	t.Setenv("PALADIN_DATA_DIR", t.TempDir())
	srv, seen := captureServer(t, "text/event-stream", chatSSE(`{"content":"ok"}`))
	p, _ := BuildProviderWithMeta(config.ApiTypeCopilot, "github-copilot", "claude-chat-only", srv.URL, "tok", chatMeta())
	tx := make(chan Chunk, 16)
	tools := []ToolDefinition{{Name: "read", Parameters: map[string]any{"type": "object"}}}
	if err := p.StreamTools(context.Background(), "", userTurn("hi"), tools, "s", tx, make(chan struct{})); err != nil {
		t.Fatal(err)
	}
	if _, has := (*seen)[0].body["tool_choice"]; has {
		t.Fatalf("tool_choice %v", (*seen)[0].body["tool_choice"])
	}
}

func TestPlainChatProvidersKeepTheirOwnSampling(t *testing.T) {
	srv, seen := captureServer(t, "text/event-stream", chatSSE(`{"content":"ok"}`))
	runStream(t, NewOpenAIProvider("litellm", "claude-sonnet-5", srv.URL, "k"), userTurn("hi"))
	for _, key := range []string{"temperature", "top_p", "n", "max_tokens", "tool_choice"} {
		if _, has := (*seen)[0].body[key]; has {
			t.Errorf("%s leaked to a non-Copilot provider", key)
		}
	}
}

func TestCopilotToolSchemasAreNormalised(t *testing.T) {
	t.Setenv("PALADIN_DATA_DIR", t.TempDir())
	srv, seen := captureServer(t, "text/event-stream", chatSSE(`{"content":"ok"}`))
	p, _ := BuildProviderWithMeta(config.ApiTypeCopilot, "github-copilot", "gemini-3.8-flash", srv.URL, "tok", chatMeta())
	tools := []ToolDefinition{
		{Name: "bare", Parameters: nil},
		{Name: "combinator", Description: "d", Parameters: map[string]any{"type": "object", "properties": map[string]any{}, "anyOf": []any{map[string]any{"required": []string{"a"}}}, "oneOf": []any{}}},
		{Name: "nullable", Description: "d", Parameters: map[string]any{"type": "object", "properties": map[string]any{"x": map[string]any{"type": []any{"string", "null"}}}}},
	}
	tx := make(chan Chunk, 16)
	if err := p.StreamTools(context.Background(), "", userTurn("hi"), tools, "s", tx, make(chan struct{})); err != nil {
		t.Fatal(err)
	}
	got := map[string]map[string]any{}
	for _, tl := range asList((*seen)[0].body["tools"]) {
		fn := asMap(asMap(tl)["function"])
		got[fn["name"].(string)] = fn
	}
	bare := asMap(got["bare"]["parameters"])
	if bare["type"] != "object" || asMap(bare["properties"]) == nil || got["bare"]["description"] == "" {
		t.Errorf("bare tool %v", got["bare"])
	}
	comb := asMap(got["combinator"]["parameters"])
	for _, k := range []string{"anyOf", "oneOf", "allOf", "not"} {
		if _, has := comb[k]; has {
			t.Errorf("top-level %s survived: %v", k, comb)
		}
	}
	x := asMap(asMap(asMap(got["nullable"]["parameters"])["properties"])["x"])
	if x["type"] != "string" || x["nullable"] != true {
		t.Errorf("Gemini nullable not rewritten: %v", x)
	}
}

func chatCacheHistory() []LlmMessage {
	id1, id2, name := "c1", "c2", "read"
	return []LlmMessage{
		{Role: "user", Content: "first prompt"},
		{Role: "assistant", Content: "first answer"},
		{Role: "user", Content: "second prompt"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: id1, Name: name, Arguments: "{}"}}},
		{Role: "tool", Content: "r1", ToolCallID: &id1, Name: &name},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: id2, Name: name, Arguments: "{}"}}},
		{Role: "tool", Content: "r2", ToolCallID: &id2, Name: &name},
	}
}

func TestCopilotChatPlacesAtMostFourCacheBreakpoints(t *testing.T) {
	t.Setenv("PALADIN_DATA_DIR", t.TempDir())
	srv, seen := captureServer(t, "text/event-stream", chatSSE(`{"content":"ok"}`))
	p, _ := BuildProviderWithMeta(config.ApiTypeCopilot, "github-copilot", "gemini-3.8-flash", srv.URL, "tok", chatMeta())
	tx := make(chan Chunk, 16)
	if err := p.StreamTools(context.Background(), "SYS", chatCacheHistory(), nil, "s", tx, make(chan struct{})); err != nil {
		t.Fatal(err)
	}
	var marked []string
	for i, m := range asList((*seen)[0].body["messages"]) {
		mm := asMap(m)
		if cc := asMap(mm["copilot_cache_control"]); cc["type"] == "ephemeral" {
			marked = append(marked, fmt.Sprintf("%d:%v", i, mm["role"]))
		}
	}
	// system, first answer (a terminal assistant message above the prompt),
	// current prompt, last tool result of the newest round.
	if len(marked) > 4 {
		t.Fatalf("too many breakpoints: %v", marked)
	}
	joined := strings.Join(marked, " ")
	for _, want := range []string{"tool", "user"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("no breakpoint on a %s message: %v", want, marked)
		}
	}
	msgs := asList((*seen)[0].body["messages"])
	if last := asMap(msgs[len(msgs)-1]); asMap(last["copilot_cache_control"])["type"] != "ephemeral" {
		t.Fatalf("the newest tool result must carry a breakpoint: %v", last)
	}
}

func TestPlainChatProvidersGetNoCacheBreakpoints(t *testing.T) {
	srv, seen := captureServer(t, "text/event-stream", chatSSE(`{"content":"ok"}`))
	runStream(t, NewOpenAIProvider("litellm", "m", srv.URL, "k"), chatCacheHistory())
	for _, m := range asList((*seen)[0].body["messages"]) {
		if _, has := asMap(m)["copilot_cache_control"]; has {
			t.Fatalf("breakpoint sent to a non-Copilot provider: %v", m)
		}
	}
}

func responsesMeta() config.ModelMeta {
	return config.ModelMeta{Protocol: protocolResponses, MaxOutput: 128000, ThinkingKnown: true, ThinkingLevels: []string{"none", "low", "medium", "high"}}
}

func TestCopilotResponsesBodyMatchesVSCode(t *testing.T) {
	t.Setenv("PALADIN_DATA_DIR", t.TempDir())
	srv, seen := captureServer(t, "text/event-stream", responsesDone(`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}`))
	p, _ := BuildProviderWithMeta(config.ApiTypeCopilot, "github-copilot", "gpt-5.6-luna", srv.URL, "tok", responsesMeta())
	tx := make(chan Chunk, 16)
	if err := p.StreamTools(context.Background(), "SYS", userTurn("hi"), nil, "s", tx, make(chan struct{})); err != nil {
		t.Fatal(err)
	}
	b := (*seen)[0].body
	if b["truncation"] != "disabled" || b["store"] != false || b["max_output_tokens"] != float64(128000) {
		t.Fatalf("body %v", b)
	}
	if asMap(b["text"])["verbosity"] != "low" {
		t.Fatalf("verbosity %v", b["text"])
	}
	if asMap(b["reasoning"])["effort"] != "medium" {
		t.Fatalf("effort must default to medium when the model declares levels: %v", b["reasoning"])
	}
	if _, has := b["instructions"]; has {
		t.Fatalf("system prompt must travel as an input message: %v", b["instructions"])
	}
	in := asList(b["input"])
	first := asMap(in[0])
	if first["role"] != "system" || asMap(asList(first["content"])[0])["text"] != "SYS" {
		t.Fatalf("input[0] %v", first)
	}
}

func TestCopilotResponsesVerbosityOnlyForTheModelsVSCodeLimits(t *testing.T) {
	t.Setenv("PALADIN_DATA_DIR", t.TempDir())
	for model, want := range map[string]bool{"gpt-5.1": true, "gpt-5-mini": true, "gpt-5.6-luna": true, "gpt-6-sol": true, "gpt-5.3-codex": false, "gpt-5.2": false} {
		srv, seen := captureServer(t, "text/event-stream", responsesDone(`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}`))
		p, _ := BuildProviderWithMeta(config.ApiTypeCopilot, "github-copilot", model, srv.URL, "tok", responsesMeta())
		runStream(t, p, userTurn("hi"))
		_, has := asMap((*seen)[0].body["text"])["verbosity"]
		if has != want {
			t.Errorf("%s: verbosity sent = %v, want %v", model, has, want)
		}
	}
}

func TestCopilotResponsesRespectsAnExplicitEffort(t *testing.T) {
	t.Setenv("PALADIN_DATA_DIR", t.TempDir())
	srv, seen := captureServer(t, "text/event-stream", responsesDone(`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}`))
	p, _ := BuildProviderWithMeta(config.ApiTypeCopilot, "github-copilot", "gpt-5.6-luna", srv.URL, "tok", responsesMeta())
	eff, err := WithEffort(p, "high")
	if err != nil {
		t.Fatal(err)
	}
	runStream(t, eff, userTurn("hi"))
	if asMap((*seen)[0].body["reasoning"])["effort"] != "high" {
		t.Fatalf("reasoning %v", (*seen)[0].body["reasoning"])
	}
}

func TestOpencodeResponsesKeepsInstructions(t *testing.T) {
	srv, seen := captureServer(t, "text/event-stream", responsesDone(`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}`))
	p := responsesProvider(t, srv)
	tx := make(chan Chunk, 16)
	if err := p.StreamTools(context.Background(), "SYS", userTurn("hi"), nil, "s", tx, make(chan struct{})); err != nil {
		t.Fatal(err)
	}
	b := (*seen)[0].body
	if b["instructions"] != "SYS" {
		t.Fatalf("OpenCode must keep instructions: %v", b)
	}
	for _, key := range []string{"truncation", "max_output_tokens", "text"} {
		if _, has := b[key]; has {
			t.Errorf("%s leaked to OpenCode", key)
		}
	}
}
