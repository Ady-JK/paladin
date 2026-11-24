package runtime

import (
	"encoding/json"
	"path/filepath"

	"github.com/Ady-JK/paladin/core/internal/agent"
	"github.com/Ady-JK/paladin/core/internal/atomicfile"
	"github.com/Ady-JK/paladin/core/internal/claude"
	"github.com/Ady-JK/paladin/core/internal/session"
)

type claudeCallUsage struct {
	Model string
	Usage claude.Raw
}
type claudeCostCheckpoint struct {
	Cost   float64            `json:"cost"`
	Models map[string]float64 `json:"models"`
}

func (c *claudeRun) noteUsage(raw claude.Raw) {
	if raw.Str("type") != "assistant" {
		return
	}
	m := raw.Obj("message")
	usage := m.Obj("usage")
	if len(usage) == 0 {
		return
	}
	if c.usageCalls == nil {
		c.usageCalls = map[string]claudeCallUsage{}
	}
	id := m.Str("id")
	if id == "" {
		id = raw.Str("uuid")
	}
	old := c.usageCalls[id]
	// Complete thinking/text blocks can share an API message id. Keep one
	// final token report, not one bill for each block.
	if old.Usage.Int("output_tokens") > usage.Int("output_tokens") {
		return
	}
	c.usageCalls[id] = claudeCallUsage{Model: m.Str("model"), Usage: usage}
}
func (c *claudeRun) persistClaudeUsage(result claude.Raw) error {
	total := result.Float("total_cost_usd")
	c.th.mu.Lock()
	delta := total - c.th.cost
	if delta < 0 {
		delta = total
	}
	previous := c.th.costModels
	c.th.cost = total
	current := map[string]float64{}
	for name, model := range result.Obj("modelUsage") {
		current[name] = claude.Decode(model).Float("costUSD")
	}
	c.th.costModels = current
	c.th.mu.Unlock()
	calls := c.usageCalls
	if len(calls) == 0 {
		calls = map[string]claudeCallUsage{"result": {Model: c.model, Usage: result.Obj("usage")}}
	}
	count := map[string]int{}
	for _, call := range calls {
		count[call.Model]++
	}
	prices := map[string]float64{}
	used := 0.0
	for model := range count {
		if before, ok := previous[model]; ok {
			price := current[model] - before
			if price < 0 {
				price = current[model]
			}
			prices[model] = price
			used += price
		}
	}
	if used == 0 {
		weight := 0.0
		for model := range count {
			weight += current[model]
		}
		if weight > 0 {
			for model := range count {
				prices[model] = delta * current[model] / weight
			}
		} else {
			prices[c.model] = delta
		}
	} else if used != delta {
		for model, price := range prices {
			prices[model] = price * delta / used
		}
	}
	for _, call := range calls {
		u := call.Usage
		model := call.Model
		if model == "" {
			model = c.model
		}
		input := u.Int("input_tokens") + u.Int("cache_read_input_tokens") + u.Int("cache_creation_input_tokens")
		output := u.Int("output_tokens")
		if input == 0 && output == 0 {
			continue
		}
		price := prices[model] / float64(max(1, count[model]))
		counters := session.Counters{PromptTokens: input, CompletionTokens: output, ReasoningTokens: u.Obj("output_tokens_details").Int("thinking_tokens"), CacheReadTokens: u.Int("cache_read_input_tokens"), CacheWriteTokens: u.Int("cache_creation_input_tokens"), TotalTokens: input + output, ListCost: price, HasCost: true, CostEstimated: true}
		session.RecordCall(c.sess.UsagePath(), c.req.SessionID, model, counters)
		c.outTokens += output
	}
	window := result.Obj("modelUsage").Obj(c.model).Int("contextWindow")
	reading := c.contextTokens
	if reading == 0 {
		u := result.Obj("usage")
		reading = u.Int("input_tokens") + u.Int("cache_read_input_tokens") + u.Int("cache_creation_input_tokens")
	}
	session.RecordContext(c.sess.UsagePath(), c.req.SessionID, c.model, session.ContextReading{Tokens: reading, Window: window})
	c.r.rememberContextWindow(HarnessClaude, c.model, window)
	c.send(agent.ChatChunk{UsageChanged: true})
	c.usageCalls = nil
	b, err := json.Marshal(claudeCostCheckpoint{Cost: total, Models: current})
	if err != nil {
		return err
	}
	return atomicfile.Write(filepath.Join(c.sess.Dir, "claude-cost.json"), b)
}
