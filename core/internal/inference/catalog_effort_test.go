package inference

import (
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/config"
)

// modelMeta is what a listing publishes for one model, as the runtime
// hands it to the inference layer.
func modelMeta(protocol string, levels ...string) config.ModelMeta {
	return config.ModelMeta{Protocol: protocol, ThinkingLevels: levels, ThinkingKnown: true}
}

// The listing's levels win over the name-based guess: the gateway knows
// which efforts it forwards for a model, the name does not. DeepSeek has
// no name rule at all, and grok's documented list stops at xhigh.
func TestCatalogLevelsBeatTheNameGuess(t *testing.T) {
	cases := []struct {
		model string
		meta  config.ModelMeta
		want  string
	}{
		{"deepseek-v4.1-flash", modelMeta(protocolChat, "low", "high", "max"), "low,high,max"},
		{"glm-5.3", modelMeta(protocolChat, "low", "high", "max"), "low,high,max"},
		{"grok-4.7", modelMeta(protocolResponses, "low", "medium", "high", "xhigh"), "low,medium,high,xhigh"},
		{"kimi-k3", modelMeta(protocolChat, "max"), "max"},
		// The name rule would give six, the listing says three.
		{"gpt-6-luna", modelMeta(protocolResponses, "none", "low", "medium", "high", "xhigh", "max"), "none,low,medium,high,xhigh,max"},
		{"claude-haiku-5-5", modelMeta(protocolChat, "low", "medium", "high"), "low,medium,high"},
	}
	for _, c := range cases {
		got := strings.Join(ModelEffortLevels(config.ApiTypeOpenai, config.OpencodeBaseURL, c.model, c.meta), ",")
		if got != c.want {
			t.Errorf("%s: got %q want %q", c.model, got, c.want)
		}
	}
}

// Without catalog metadata the old name-based rules still decide, so a
// provider that publishes nothing keeps working as before.
func TestNoMetadataKeepsTheNameGuess(t *testing.T) {
	const lite = "http://127.0.0.1:4000/v1"
	cases := map[string]string{
		"claude-sonnet-5": "none,low,medium,high,xhigh,max",
		"gpt-5":           "minimal,low,medium,high",
		"gpt-4o":          "",
	}
	for model, want := range cases {
		got := strings.Join(ModelEffortLevels(config.ApiTypeOpenai, lite, model, config.ModelMeta{}), ",")
		if got != want {
			t.Errorf("%s: got %q want %q", model, got, want)
		}
	}
	// OpenCode's own routing still applies to an un-described model: a
	// Claude name there is served over Messages, which carries no effort.
	if got := ModelEffortLevels(config.ApiTypeOpenai, config.OpencodeBaseURL, "claude-sonnet-5", config.ModelMeta{}); got != nil {
		t.Fatalf("got %v", got)
	}
}

// A model the listing says has no reasoning gets no levels, even when
// its name looks like a reasoning model.
func TestCatalogCanTakeLevelsAway(t *testing.T) {
	meta := config.ModelMeta{ThinkingKnown: true}
	if got := ModelEffortLevels(config.ApiTypeOpenai, config.OpencodeBaseURL, "claude-sonnet-5", meta); got != nil {
		t.Fatalf("got %v", got)
	}
}

// The pickable levels are only offered where the request can carry an
// effort: the Anthropic and Gemini dialects cannot yet, so their models
// hide the chip rather than send a setting into a void.
func TestLevelsOnlyForDialectsThatCarryThem(t *testing.T) {
	for _, protocol := range []string{protocolAnthropic, protocolGoogle} {
		meta := modelMeta(protocol, "low", "high")
		if got := ModelEffortLevels(config.ApiTypeOpenai, config.OpencodeBaseURL, "claude-haiku-5-5", meta); got != nil {
			t.Fatalf("%s: got %v", protocol, got)
		}
	}
}

// The provider built for a model must route the way its listing says,
// not the way its name looks.
func TestCatalogProtocolRoutesTheProvider(t *testing.T) {
	// qwen3.8-max is served over Anthropic Messages although the name
	// has never been in the name-based table.
	meta := config.ModelMeta{Protocol: protocolAnthropic, ThinkingKnown: true}
	p := NewOpenAIProviderWithMeta("oc", "qwen3.8-max", config.OpencodeBaseURL, "k", meta)
	if p.protocol != protocolAnthropic {
		t.Fatalf("protocol %q", p.protocol)
	}
	// And a name that guessed wrong is corrected: a chat model named
	// gpt-… on a gateway that serves it over chat completions.
	meta = config.ModelMeta{Protocol: protocolChat, ThinkingKnown: true}
	p = NewOpenAIProviderWithMeta("oc", "gpt-6-luna", config.OpencodeBaseURL, "k", meta)
	if p.protocol != protocolChat {
		t.Fatalf("protocol %q", p.protocol)
	}
	// A plain provider ignores catalog protocol hints entirely.
	p = NewOpenAIProviderWithMeta("openai", "gpt-5", "https://api.openai.com/v1", "k", meta)
	if p.protocol != protocolChat {
		t.Fatalf("plain openai protocol %q", p.protocol)
	}
	// Nonsense from a listing falls back to the name rule.
	p = NewOpenAIProviderWithMeta("oc", "gpt-6-luna", config.OpencodeBaseURL, "k", modelMeta("telepathy"))
	if p.protocol != protocolResponses {
		t.Fatalf("protocol %q", p.protocol)
	}
}

// The published window beats the static table, including for a model the
// table has never heard of.
func TestCatalogWindowBeatsTheTable(t *testing.T) {
	if w, known := WindowFor(config.ModelMeta{ContextWindow: 1234567}, "glm-5.3"); !known || w != 1234567 {
		t.Fatalf("got %d known=%v", w, known)
	}
	// omen-alpha has no table entry: the listing is the only source.
	if w, known := WindowFor(config.ModelMeta{ContextWindow: 999}, "omen-alpha"); !known || w != 999 {
		t.Fatalf("got %d known=%v", w, known)
	}
	// With no published window the table still answers.
	if w, known := WindowFor(config.ModelMeta{}, "glm-5.3"); !known || w != 1000000 {
		t.Fatalf("got %d known=%v", w, known)
	}
	if w, known := WindowFor(config.ModelMeta{}, "omen-alpha"); known || w != DefaultContextWindow {
		t.Fatalf("got %d known=%v", w, known)
	}
}

// A provider built with metadata carries the levels into the wire, and
// the reject-and-retry net still covers a level the gateway refuses.
func TestMetadataLevelsReachTheWire(t *testing.T) {
	srv, body := capture(t, "application/json", chatReply)
	meta := modelMeta(protocolChat, "low", "high", "max")
	base := NewOpenAIProviderWithMeta("oc", "deepseek-v4.1-flash", srv.URL, "k", meta)
	if got := strings.Join(base.EffortLevels(), ","); got != "low,high,max" {
		t.Fatalf("levels %q", got)
	}
	if _, err := WithEffort(base, "max"); err != nil {
		t.Fatal(err)
	}
	p, err := WithEffort(base, "max")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.CompleteTools(t.Context(), "", []LlmMessage{{Role: "user", Content: "hi"}}, nil, "s"); err != nil {
		t.Fatal(err)
	}
	if (*body)["reasoning_effort"] != "max" {
		t.Fatalf("body %v", *body)
	}
	// A level the listing did not offer is refused, not sent and dropped.
	if _, err := WithEffort(base, "xhigh"); err == nil {
		t.Fatal("xhigh is not one of the model's levels")
	}
}
