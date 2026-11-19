package runtime

import (
	"github.com/Ady-JK/paladin/core/internal/agent"
	"github.com/Ady-JK/paladin/core/internal/codex"
	"github.com/Ady-JK/paladin/core/internal/session"
)

// codexTokens is one of Codex's token breakdowns. Codex counts cache reads and
// writes inside the input figure.
type codexTokens struct {
	input, cached, cacheWrite, output, reasoning, total int64
}

func tokensOf(r codex.Raw) codexTokens {
	return codexTokens{input: r.Int("inputTokens"), cached: r.Int("cachedInputTokens"), cacheWrite: r.Int("cacheWriteInputTokens"),
		output: r.Int("outputTokens"), reasoning: r.Int("reasoningOutputTokens"), total: r.Int("totalTokens")}
}

// below reports a counter that went down, which means the thread's total was
// reset (a compaction, a new process) and cannot be diffed.
func (t codexTokens) below(prev codexTokens) bool {
	return t.input < prev.input || t.cached < prev.cached || t.output < prev.output || t.reasoning < prev.reasoning || t.total < prev.total
}

func (t codexTokens) minus(prev codexTokens) codexTokens {
	return codexTokens{input: t.input - prev.input, cached: t.cached - prev.cached, cacheWrite: max(t.cacheWrite-prev.cacheWrite, 0),
		output: t.output - prev.output, reasoning: t.reasoning - prev.reasoning, total: t.total - prev.total}
}

func (t codexTokens) any() bool {
	return t.input > 0 || t.cached > 0 || t.output > 0 || t.reasoning > 0
}

// billable is what a usage notice adds to the ledger. `total` is the thread's
// running sum and `last` its newest model response; billing the growth of the
// sum keeps a repeated notice (Codex resends one when nothing new was billed)
// from being counted twice, and falls back to `last` when there is nothing to
// diff against.
func (th *codexThread) billable(usage codex.Raw) codexTokens {
	last := tokensOf(usage.Obj("last"))
	if !usage.Has("total") || th == nil {
		return last
	}
	current := tokensOf(usage.Obj("total"))
	prev := th.usageBase
	th.usageBase = &current
	if prev == nil || current.below(*prev) {
		return last
	}
	return current.minus(*prev)
}

// usage applies one `thread/tokenUsage/updated`: the growth is billed, and the
// newest response's size and Codex's own window drive the context gauge.
func (c *codexRun) usage(usage codex.Raw) {
	bill := c.thread.billable(usage)
	reading := session.ContextReading{Tokens: tokensOf(usage.Obj("last")).total, Window: usage.Int("modelContextWindow")}
	model := c.model
	if bill.any() {
		c.outTokens += bill.output
		call := session.Counters{
			PromptTokens: bill.input, CompletionTokens: bill.output, ReasoningTokens: bill.reasoning,
			CacheReadTokens: bill.cached, CacheWriteTokens: bill.cacheWrite, TotalTokens: bill.total,
		}
		// Codex counts cached input inside its input tokens and always reports it.
		if bill.input > 0 {
			call.CacheHitCalls, call.CacheHitSum = 1, session.CacheShare(bill.input, bill.cached)
		}
		session.RecordCallWithContext(c.usagePath, c.sessionID, model, call, reading)
	} else {
		session.RecordContext(c.usagePath, c.sessionID, model, reading)
	}
	c.r.rememberContextWindow(HarnessCodex, model, reading.Window)
	c.send(agent.ChatChunk{UsageChanged: true})
}

// rememberContextWindow keeps the window a harness reported for a model, so a
// session that has not run yet can show it.
func (r *AgentRuntime) rememberContextWindow(harness, model string, window int64) {
	if model == "" || window <= 0 || r.loadSettings().ContextWindows[harness][model] == window {
		return
	}
	_ = r.updateSettings(func(s *Settings) {
		if s.ContextWindows == nil {
			s.ContextWindows = map[string]map[string]int64{}
		}
		if s.ContextWindows[harness] == nil {
			s.ContextWindows[harness] = map[string]int64{}
		}
		s.ContextWindows[harness][model] = window
	})
}
