// Package inference: models.go is the static per-model context window
// table. The OpenCode catalog returns bare ids (no sizes), so windows are
// sourced from the OpenRouter model list where an exact or same-family
// match exists; the rest fall back to DefaultContextWindow until real
// values are supplied. Every entry documents its source so guesses can
// be corrected without re-researching the whole table.
package inference

// DefaultContextWindow is the assumed window for models with no table
// entry. It is deliberately marked unknown by ContextWindow so the UI
// can show tokens without a misleading fill percentage.
const DefaultContextWindow int64 = 200000

type windowEntry struct {
	tokens int64
	// source is "opencode" (exact id match on OpenRouter), "family"
	// (same-family best effort), or "fallback".
	source string
}

var contextWindows = map[string]windowEntry{
	// Exact OpenRouter id matches.
	"claude-haiku-5-5": {1000000, "opencode"},
	"gpt-5.6-luna":     {1050000, "opencode"},
	"gpt-6-luna":       {1050000, "opencode"},
	"grok-4.6":         {500000, "opencode"},
	"kimi-k2.6":        {262144, "opencode"},
	"kimi-k2.7-code":   {262144, "opencode"},
	"longcat-2.0":      {1048756, "opencode"},
	"mimo-v2.5":        {1050000, "opencode"},
	"mimo-v2.5-pro":    {1050000, "opencode"},
	"mimo-v2.6-flash":  {1050000, "opencode"},
	"mimo-v2.6-pro":    {1050000, "opencode"},
	"minimax-m2.5":     {204800, "opencode"},
	"qwen3.6-plus":     {1000000, "opencode"},
	"qwen3.7-max":      {1000000, "opencode"},
	"qwen3.7-plus":     {1000000, "opencode"},
	"qwen3.8-flash":    {1000000, "opencode"},
	"qwen3.8-max":      {1000000, "opencode"},
	"step-5-preview":   {1048576, "opencode"},
	// Same-family best effort (no exact id on OpenRouter).
	"deepseek-v4-pro":     {163840, "family"},
	"deepseek-v4.1-flash": {163840, "family"},
	"gemini-4-argon":      {1048576, "family"},
	"glm-5.1":             {204800, "family"},
	"glm-5.2":             {204800, "family"},
	"glm-5.3":             {204800, "family"},
	"glm-5.3-flash":       {204800, "family"},
	"grok-4.7":            {500000, "family"},
	"kimi-k3":             {262144, "family"},
	"kimi-k3.1-pro":       {262144, "family"},
	"minimax-m2.7":        {204800, "family"},
	"minimax-m3":          {204800, "family"},
	"minimax-m3.1-flash":  {204800, "family"},
	// beam-free, muse-spark-*, omen-*, hy*, iceman-*: no OpenRouter
	// counterpart; they resolve to DefaultContextWindow (known=false).
}

// ContextWindow returns the model's total context window in tokens.
// known is false for models with no table entry; callers must not show
// a fill percentage then, only raw token counts.
func ContextWindow(model string) (tokens int64, known bool) {
	if e, ok := contextWindows[model]; ok {
		return e.tokens, true
	}
	return DefaultContextWindow, false
}
