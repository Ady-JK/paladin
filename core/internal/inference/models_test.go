package inference

import "testing"

func TestContextWindowExactAndFamily(t *testing.T) {
	cases := map[string]int64{
		// Exact OpenRouter id matches.
		"claude-haiku-5-5": 1000000,
		"gpt-5.6-luna":     1050000,
		"gpt-6-luna":       1050000,
		"grok-4.6":         500000,
		"kimi-k2.6":        262144,
		"kimi-k2.7-code":   262144,
		"longcat-2.0":      1048756,
		"mimo-v2.5":        1050000,
		"minimax-m2.5":     204800,
		"qwen3.8-flash":    1000000,
		"step-5-preview":   1048576,
		// Same-family best effort.
		"deepseek-v4.1-flash": 163840,
		"gemini-4-argon":      1048576,
		"glm-5.3-flash":       204800,
		"grok-4.7":            500000,
		"qwen3.7-max":         1000000,
	}
	for model, want := range cases {
		got, known := ContextWindow(model)
		if !known || got != want {
			t.Fatalf("%s: got %d known=%v want %d", model, got, known, want)
		}
	}
}

func TestContextWindowUnknownFallsBack(t *testing.T) {
	for _, model := range []string{"", "beam-free", "muse-spark-1.3-contributor", "omen-alpha", "hy3", "iceman-contributor-free", "something-new"} {
		got, known := ContextWindow(model)
		if known {
			t.Fatalf("%s: should be unknown", model)
		}
		if got != DefaultContextWindow {
			t.Fatalf("%s: got %d want default %d", model, got, DefaultContextWindow)
		}
	}
}
