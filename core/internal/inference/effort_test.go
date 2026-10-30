package inference

import (
	"strings"
	"testing"
)

func TestEffortLevelsFollowTheModelFamily(t *testing.T) {
	cases := map[string]string{
		"claude-opus-5-5":                      "none,low,medium,high,xhigh,max",
		"claude-sonnet-5":                      "none,low,medium,high,xhigh,max",
		"anthropic/claude-opus-4.6":            "none,low,medium,high,max",
		"claude-haiku-4-5-20251001":            "none,low,medium,high",
		"claude-3-7-sonnet-latest":             "none,low,medium,high",
		"claude-3-5-sonnet":                    "",
		"gpt-5":                                "minimal,low,medium,high",
		"openai/gpt-5.2-codex":                 "none,low,medium,high,xhigh",
		"o3-mini":                              "low,medium,high",
		"gemini-2.5-pro":                       "low,medium,high",
		"gemini-2.5-flash":                     "none,low,medium,high",
		"gpt-4o-mini":                          "",
		"llama-3.1-70b":                        "",
		"vertex_ai/claude-opus-5@20260101":     "none,low,medium,high,xhigh,max",
		"bedrock/us.anthropic.claude-sonnet-5": "none,low,medium,high,xhigh,max",
	}
	for model, want := range cases {
		if got := strings.Join(EffortLevels(protocolChat, model), ","); got != want {
			t.Errorf("%s: got %q want %q", model, got, want)
		}
	}
}

func TestEffortOnlyWhereTheDialectCarriesIt(t *testing.T) {
	if EffortLevels(protocolAnthropic, "claude-sonnet-5") != nil || EffortLevels(protocolGoogle, "gemini-2.5-pro") != nil {
		t.Fatal("native anthropic/google requests do not carry an effort yet")
	}
	if strings.Join(EffortLevels(protocolResponses, "gpt-5"), ",") != "minimal,low,medium,high" {
		t.Fatal("responses carries the effort")
	}
}

func TestProviderEffortLevelsAndValidation(t *testing.T) {
	p := NewOpenAIProvider("lite", "claude-sonnet-5", "http://localhost:4000/v1", "k")
	if len(p.EffortLevels()) != 6 {
		t.Fatalf("levels %v", p.EffortLevels())
	}
	if err := ValidEffort(p.EffortLevels(), "ultra"); err == nil {
		t.Fatal("unknown level must be rejected")
	}
	if err := ValidEffort(p.EffortLevels(), "high"); err != nil {
		t.Fatal(err)
	}
	if err := ValidEffort(p.EffortLevels(), ""); err != nil {
		t.Fatal("empty means the provider default")
	}
}
