package inference

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Ady-JK/paladin/core/internal/config"
)

// liveThinkingPrompt needs real reasoning before the tool call.
const liveThinkingPrompt = "Let a be the number of primes between 1000 and 1100 and b the 12th Fibonacci number. " +
	"Work these out carefully first (do not show work), then call the add tool with a and b. " +
	"After the tool result, answer with the sum only."

type liveRound struct {
	text   string
	calls  []ToolCall
	think  *Thinking
	prompt int64
}

func liveStream(t *testing.T, p Provider, messages []LlmMessage, tools []ToolDefinition) liveRound {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	tx := make(chan Chunk, 4096)
	done := make(chan struct{})
	err := p.StreamTools(ctx, "", messages, tools, "thinking-live", tx, done)
	close(done)
	close(tx)
	if err != nil {
		t.Fatalf("%s: %v", p.Model(), err)
	}
	var out liveRound
	for c := range tx {
		out.text += c.Text
		if c.Thinking != nil {
			out.think = c.Thinking
		}
		if len(c.ToolCalls) > 0 {
			out.calls = c.ToolCalls
		}
		if c.Usage != nil {
			out.prompt = c.Usage.PromptTokens
			if op, ok := p.(*OpenAIProvider); ok && op.protocol == protocolAnthropic {
				// Messages reports cached input apart from input_tokens.
				out.prompt += c.Usage.CacheReadTokens + c.Usage.CacheWriteTokens
			}
		}
	}
	return out
}

// This opt-in test replays real reasoning through the signed-in Copilot
// account: it must be consumed by the model that made it, survive a
// follow-up turn, and be re-sent as readable text after a model switch.
func TestCopilotLiveReasoningReplay(t *testing.T) {
	if os.Getenv("PALADIN_COPILOT_LIVE_TEST") != "1" {
		t.Skip("set PALADIN_COPILOT_LIVE_TEST=1 to test the signed-in Copilot account")
	}
	raw, err := os.ReadFile(filepath.Join(config.DataDir(), "config", "providers.json"))
	if err != nil {
		t.Fatal(err)
	}
	var settings config.ProvidersFile
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatal(err)
	}
	baseURL := ""
	for _, p := range settings.Providers {
		if p.ApiType == config.ApiTypeCopilot && p.Name == "github-copilot" {
			baseURL = p.ApiURL
		}
	}
	if !strings.HasPrefix(baseURL, "https://api.") || !strings.HasSuffix(baseURL, ".githubcopilot.com") {
		t.Fatal("no trusted Copilot endpoint")
	}
	key, ok, err := (config.OsKeyStore{}).Get("github-copilot")
	if err != nil || !ok || key == "" {
		t.Fatal("Copilot sign-in is unavailable")
	}
	tools := []ToolDefinition{{Name: "add", Description: "add two integers", Parameters: map[string]any{
		"type":       "object",
		"properties": map[string]any{"a": map[string]any{"type": "integer"}, "b": map[string]any{"type": "integer"}},
		"required":   []string{"a", "b"},
	}}}
	models := []struct{ name, effort, format string }{
		{"claude-sonnet-5.5", "high", thinkingMessages}, {"claude-haiku-4.5", "", ""}, {"gemini-3.8-flash", "high", thinkingCopilotChat}, {"gpt-5.6-luna", "medium", thinkingResponses},
	}
	catalogProvider, err := BuildProvider(config.ApiTypeCopilot, "github-copilot", "", baseURL, key)
	if err != nil {
		t.Fatal(err)
	}
	_, catalog, err := catalogProvider.(*OpenAIProvider).ListCatalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	build := func(model, effort string) Provider {
		base, err := BuildProviderWithMeta(config.ApiTypeCopilot, "github-copilot", model, baseURL, key, catalog[model])
		if err != nil {
			t.Fatal(err)
		}
		p, err := WithEffort(base, effort)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	history := func(th *Thinking, calls []ToolCall) []LlmMessage {
		id, name := calls[0].ID, "add"
		return []LlmMessage{
			{Role: "user", Content: liveThinkingPrompt},
			{Role: "assistant", Thinking: th, ToolCalls: calls},
			{Role: "tool", Content: "160", ToolCallID: &id, Name: &name},
		}
	}
	for _, m := range models {
		t.Run(m.name, func(t *testing.T) {
			p := build(m.name, m.effort)
			first := liveStream(t, p, []LlmMessage{{Role: "user", Content: liveThinkingPrompt}}, tools)
			if len(first.calls) == 0 {
				t.Fatalf("no tool call: %q", first.text)
			}
			if m.format != "" && (first.think == nil || first.think.Format != m.format || first.think.Model != m.name) {
				t.Fatalf("reasoning was not captured as %s: %+v", m.format, first.think)
			}
			if first.think != nil {
				t.Logf("captured format=%s opaqueLen=%d rawLen=%d", first.think.Format, len(first.think.Opaque), len(first.think.Raw))
			} else {
				t.Log("this model produced no reasoning for the prompt")
			}

			with := liveStream(t, p, history(first.think, first.calls), tools)
			without := liveStream(t, p, history(nil, first.calls), tools)
			if !strings.Contains(with.text, "160") {
				t.Fatalf("answer %q", with.text)
			}
			if first.think != nil && with.prompt <= without.prompt {
				t.Fatalf("model did not read the replayed reasoning: prompt %d with vs %d without", with.prompt, without.prompt)
			}
			t.Logf("prompt tokens %d with reasoning vs %d without", with.prompt, without.prompt)

			// A follow-up turn after a text answer that carries its own reasoning.
			answerThink := with.think
			follow := append(history(first.think, first.calls),
				LlmMessage{Role: "assistant", Content: with.text, Thinking: answerThink},
				LlmMessage{Role: "user", Content: "Now say the word done."})
			if res := liveStream(t, p, follow, tools); strings.TrimSpace(res.text) == "" && len(res.calls) == 0 {
				t.Fatal("empty reply to the follow-up turn")
			}

			// Another model must accept the history without this model's state.
			for _, other := range models {
				if other.name == m.name {
					continue
				}
				res := liveStream(t, build(other.name, other.effort), history(first.think, first.calls), tools)
				if !strings.Contains(res.text, "160") {
					t.Fatalf("%s after %s: %q", other.name, m.name, res.text)
				}
			}
		})
	}
}

// This opt-in test replays real reasoning through the local LiteLLM proxy:
// signed thinking blocks must be captured and consumed by the model.
func TestLiteLLMLiveReasoningReplay(t *testing.T) {
	if os.Getenv("PALADIN_LITELLM_LIVE_TEST") != "1" {
		t.Skip("set PALADIN_LITELLM_LIVE_TEST=1 to test the local LiteLLM proxy")
	}
	raw, err := os.ReadFile(filepath.Join(config.DataDir(), "config", "providers.json"))
	if err != nil {
		t.Fatal(err)
	}
	var settings config.ProvidersFile
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatal(err)
	}
	baseURL := ""
	for _, p := range settings.Providers {
		if p.Name == "litellm" {
			baseURL = p.ApiURL
		}
	}
	if !strings.HasPrefix(baseURL, "http://127.0.0.1:") && !strings.HasPrefix(baseURL, "http://localhost:") {
		t.Fatal("litellm is not a local proxy")
	}
	key, _, err := (config.OsKeyStore{}).Get("litellm")
	if err != nil {
		t.Fatal(err)
	}
	base, err := BuildProvider(config.ApiTypeOpenai, "litellm", "claude-sonnet-5", baseURL, key)
	if err != nil {
		t.Fatal(err)
	}
	p, err := WithEffort(base, "high")
	if err != nil {
		t.Fatal(err)
	}
	tools := []ToolDefinition{{Name: "add", Description: "add two integers", Parameters: map[string]any{
		"type":       "object",
		"properties": map[string]any{"a": map[string]any{"type": "integer"}, "b": map[string]any{"type": "integer"}},
		"required":   []string{"a", "b"},
	}}}
	first := liveStream(t, p, []LlmMessage{{Role: "user", Content: liveThinkingPrompt}}, tools)
	if len(first.calls) == 0 {
		t.Fatalf("no tool call: %q", first.text)
	}
	if first.think == nil || first.think.Format != thinkingBlocks || len(first.think.Raw) == 0 {
		t.Fatalf("signed thinking blocks were not captured: %+v", first.think)
	}
	id, name := first.calls[0].ID, "add"
	history := func(th *Thinking) []LlmMessage {
		return []LlmMessage{
			{Role: "user", Content: liveThinkingPrompt},
			{Role: "assistant", Thinking: th, ToolCalls: first.calls},
			{Role: "tool", Content: "160", ToolCallID: &id, Name: &name},
		}
	}
	with := liveStream(t, p, history(first.think), tools)
	without := liveStream(t, p, history(nil), tools)
	if !strings.Contains(with.text, "160") {
		t.Fatalf("answer %q", with.text)
	}
	if with.prompt <= without.prompt {
		t.Fatalf("model did not read the replayed blocks: prompt %d with vs %d without", with.prompt, without.prompt)
	}
	t.Logf("blocks captured (%d bytes); prompt tokens %d with vs %d without", len(first.think.Raw), with.prompt, without.prompt)
}
