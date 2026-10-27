package inference

import "testing"

func TestOpenCodeProtocolMapping(t *testing.T) {
	cases := map[string]string{
		"deepseek-v4.1-flash":        protocolChat,
		"glm-5.3-flash":              protocolChat,
		"gpt-6-luna":                 protocolResponses,
		"grok-4.7":                   protocolResponses,
		"muse-spark-1.3-contributor": protocolResponses,
		"claude-haiku-5-5":           protocolAnthropic,
		"gemini-4-argon":             protocolGoogle,
		"unknown-model":              protocolChat,
	}
	for model, want := range cases {
		if got := opencodeProtocol(model); got != want {
			t.Fatalf("%s: got %q want %q", model, got, want)
		}
	}
}

func TestOpenCodeGatewayURLs(t *testing.T) {
	cases := []struct {
		base, anthropic, google string
	}{
		{
			"https://opencode.ai/inference/go/openai/v1",
			"https://opencode.ai/inference/go/anthropic/v1/messages",
			"https://opencode.ai/inference/go/google/v1beta/models/gemini-4-argon:generateContent",
		},
		{
			"https://opencode.ai/zen/go/v1",
			"https://opencode.ai/zen/go/anthropic/v1/messages",
			"https://opencode.ai/zen/go/google/v1beta/models/gemini-4-argon:generateContent",
		},
	}
	for _, c := range cases {
		p := NewOpenAIProvider("oc", "gemini-4-argon", c.base, "k")
		if got := p.anthropicURL(); got != c.anthropic {
			t.Fatalf("%s anthropic: got %s", c.base, got)
		}
		if got := p.googleURL(false); got != c.google {
			t.Fatalf("%s google: got %s", c.base, got)
		}
		if p.protocol != protocolGoogle {
			t.Fatalf("%s protocol %q", c.base, p.protocol)
		}
	}
}

func TestNewOpenAIProviderDoesNotRoutePlainOpenAI(t *testing.T) {
	p := NewOpenAIProvider("openai", "gpt-6-luna", "https://api.openai.com/v1", "k")
	if p.protocol != protocolChat {
		t.Fatalf("plain openai routed to %q", p.protocol)
	}
}
