package inference

import "testing"

func TestContextWindowExact(t *testing.T) {
	cases := map[string]int64{
		"beam-free":                  1048576,
		"claude-haiku-5-5":           1000000,
		"deepseek-v4-pro":            1000000,
		"deepseek-v4.1-flash":        1000000,
		"gemini-4-argon":             1048576,
		"glm-5.1":                    204800,
		"glm-5.2":                    1000000,
		"glm-5.3":                    1000000,
		"glm-5.3-flash":              1000000,
		"gpt-5.6-luna":               1050000,
		"gpt-6-luna":                 1050000,
		"grok-4.6":                   500000,
		"grok-4.7":                   500000,
		"hy3":                        256000,
		"hy4-preview":                1024000,
		"iceman-contributor-free":    262144,
		"kimi-k2.6":                  262144,
		"kimi-k2.7-code":             262144,
		"kimi-k3":                    1048576,
		"kimi-k3.1-pro":              1048576,
		"longcat-2.0":                1000000,
		"mimo-v2.5":                  1000000,
		"mimo-v2.5-pro":              1048576,
		"mimo-v2.6-flash":            1048576,
		"mimo-v2.6-pro":              1048576,
		"minimax-m2.5":               204800,
		"minimax-m2.7":               204800,
		"minimax-m3":                 512000,
		"minimax-m3.1-flash":         1000000,
		"muse-spark-1.2-contributor": 1048576,
		"muse-spark-1.3-contributor": 1048576,
		"qwen3.6-plus":               262144,
		"qwen3.7-plus":               1000000,
		"qwen3.8-flash":              1000000,
		"qwen3.8-max":                262144,
		"step-5-preview":             1000000,
	}
	for model, want := range cases {
		got, known := ContextWindow(model)
		if !known || got != want {
			t.Fatalf("%s: got %d known=%v want %d", model, got, known, want)
		}
	}
}

func TestContextWindowUnknownFallsBack(t *testing.T) {
	for _, model := range []string{"", "omen-alpha", "qwen3.7-max", "something-new"} {
		got, known := ContextWindow(model)
		if known {
			t.Fatalf("%s: should be unknown", model)
		}
		if got != DefaultContextWindow {
			t.Fatalf("%s: got %d want default %d", model, got, DefaultContextWindow)
		}
	}
}
