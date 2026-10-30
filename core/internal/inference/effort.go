package inference

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/Ady-JK/paladin/core/internal/config"
)

// Reasoning efforts, as sent on the wire (OpenAI reasoning_effort; LiteLLM
// maps them onto Anthropic and Gemini thinking). "none" turns thinking off;
// an empty effort sends nothing and leaves the provider's default.
const (
	EffortNone    = "none"
	EffortMinimal = "minimal"
	EffortLow     = "low"
	EffortMedium  = "medium"
	EffortHigh    = "high"
	EffortXHigh   = "xhigh"
	EffortMax     = "max"
)

var (
	claudeFamilyFirst  = regexp.MustCompile(`(?i)claude-(opus|sonnet|haiku)-(\d+)(?:[.-](\d{1,2}))?(?:\D|$)`)
	claudeVersionFirst = regexp.MustCompile(`(?i)claude-(\d+)(?:[.-](\d{1,2}))?-(opus|sonnet|haiku)`)
	gptVersion         = regexp.MustCompile(`(?i)(?:^|/)gpt-(\d+)(?:\.(\d+))?`)
	oSeries            = regexp.MustCompile(`(?i)(?:^|/)o[134](?:-|$)`)
	geminiVersion      = regexp.MustCompile(`(?i)gemini-(\d+)(?:\.(\d+))?`)
)

// EffortLevels lists the efforts a model accepts, weakest first, judged
// from its name (as opencode does without a catalog). Nil means the model
// has no adjustable thinking, or the dialect cannot carry an effort yet.
func EffortLevels(protocol, model string) []string {
	if protocol != protocolChat && protocol != protocolResponses {
		return nil
	}
	if major, minor, ok := claudeVersion(model); ok {
		switch {
		case major > 4 || (major == 4 && minor >= 7):
			return []string{EffortNone, EffortLow, EffortMedium, EffortHigh, EffortXHigh, EffortMax}
		case major == 4 && minor == 6:
			return []string{EffortNone, EffortLow, EffortMedium, EffortHigh, EffortMax}
		case major == 4 || (major == 3 && minor == 7):
			return []string{EffortNone, EffortLow, EffortMedium, EffortHigh}
		}
		return nil
	}
	if m := gptVersion.FindStringSubmatch(model); m != nil {
		major, minor := atoi(m[1]), atoi(m[2])
		switch {
		case major > 5 || (major == 5 && minor >= 2):
			return []string{EffortNone, EffortLow, EffortMedium, EffortHigh, EffortXHigh}
		case major == 5 && minor == 1:
			return []string{EffortNone, EffortLow, EffortMedium, EffortHigh}
		case major == 5:
			return []string{EffortMinimal, EffortLow, EffortMedium, EffortHigh}
		}
		return nil
	}
	if oSeries.MatchString(model) {
		return []string{EffortLow, EffortMedium, EffortHigh}
	}
	if m := geminiVersion.FindStringSubmatch(model); m != nil {
		major, minor := atoi(m[1]), atoi(m[2])
		if major < 2 || (major == 2 && minor < 5) {
			return nil
		}
		if strings.Contains(strings.ToLower(model), "flash") {
			return []string{EffortNone, EffortLow, EffortMedium, EffortHigh}
		}
		return []string{EffortLow, EffortMedium, EffortHigh}
	}
	return nil
}

func claudeVersion(model string) (major, minor int, ok bool) {
	if m := claudeFamilyFirst.FindStringSubmatch(model); m != nil {
		return atoi(m[2]), atoi(m[3]), true
	}
	if m := claudeVersionFirst.FindStringSubmatch(model); m != nil {
		return atoi(m[1]), atoi(m[2]), true
	}
	return 0, 0, false
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// ValidEffort accepts "" (provider default) or one of levels.
func ValidEffort(levels []string, effort string) error {
	if effort == "" {
		return nil
	}
	for _, l := range levels {
		if l == effort {
			return nil
		}
	}
	if len(levels) == 0 {
		return fmt.Errorf("this model has no adjustable thinking")
	}
	return fmt.Errorf("thinking level %q is not one of %s", effort, strings.Join(levels, ", "))
}

// EffortLevels is the provider's model's level list.
func (p *OpenAIProvider) EffortLevels() []string {
	return EffortLevels(p.protocol, p.model)
}

// WithEffort returns provider p sending effort with every call; "" is p
// itself. The effort must be one p's model accepts.
func WithEffort(p Provider, effort string) (Provider, error) {
	if effort == "" {
		return p, nil
	}
	op, ok := p.(*OpenAIProvider)
	if !ok {
		return nil, fmt.Errorf("provider %s cannot set a thinking level", p.Name())
	}
	if err := ValidEffort(op.EffortLevels(), effort); err != nil {
		return nil, err
	}
	cp := *op
	cp.effort, cp.effortDropped = effort, &atomic.Bool{}
	return &cp, nil
}

// deltaReasoning pulls thinking text out of a chat message or delta:
// LiteLLM and DeepSeek name it reasoning_content, OpenRouter reasoning.
func deltaReasoning(raw string) string {
	if !strings.Contains(raw, `"reasoning`) {
		return ""
	}
	var d struct {
		ReasoningContent *string `json:"reasoning_content"`
		Reasoning        *string `json:"reasoning"`
	}
	if json.Unmarshal([]byte(raw), &d) != nil {
		return ""
	}
	if d.ReasoningContent != nil && *d.ReasoningContent != "" {
		return *d.ReasoningContent
	}
	if d.Reasoning != nil {
		return *d.Reasoning
	}
	return ""
}

// CompleteTools runs the call with the provider's thinking level; a
// level the provider rejects is dropped (see effortRejected) and the
// call retried once without it.
func (p *OpenAIProvider) CompleteTools(ctx context.Context, system string, messages []LlmMessage, tools []ToolDefinition, sessionID string) (ToolResponse, error) {
	q := p.live()
	res, err := q.completeVia(ctx, system, messages, tools, sessionID)
	if q.effort != "" && effortRejected(err) {
		p.dropEffort(err)
		return p.live().completeVia(ctx, system, messages, tools, sessionID)
	}
	return res, err
}

// StreamTools is the streaming counterpart of CompleteTools. A rejected
// level fails before anything streams, so the retry starts clean.
func (p *OpenAIProvider) StreamTools(ctx context.Context, system string, messages []LlmMessage, tools []ToolDefinition, sessionID string, tx chan<- Chunk, done <-chan struct{}) error {
	q := p.live()
	err := q.streamVia(ctx, system, messages, tools, sessionID, tx, done)
	if q.effort == "" || !effortRejected(err) {
		return err
	}
	p.dropEffort(err)
	select {
	case tx <- Chunk{EffortDropped: q.effort}:
	case <-done:
		return CancelledError()
	case <-ctx.Done():
		return CancelledError()
	}
	return p.live().streamVia(ctx, system, messages, tools, sessionID, tx, done)
}

// live is p as it should call now: without its effort once dropped.
func (p *OpenAIProvider) live() *OpenAIProvider {
	if p.effort == "" || p.effortDropped == nil || !p.effortDropped.Load() {
		return p
	}
	cp := *p
	cp.effort = ""
	return &cp
}

func (p *OpenAIProvider) dropEffort(err error) {
	if p.effortDropped != nil && !p.effortDropped.Swap(true) {
		log.Printf("inference: %s rejected thinking level %q for %s; sending none: %v", p.name, p.effort, p.model, err)
	}
}

// effortRejected reports a 400/422 that blames the thinking parameters,
// as opposed to the prompt or the tools.
func effortRejected(err error) bool {
	var pe *ProviderError
	if !errors.As(err, &pe) || (pe.Status != 400 && pe.Status != 422) {
		return false
	}
	msg := strings.ToLower(pe.Message)
	for _, s := range []string{"reasoning", "thinking", "effort", "budget_tokens"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

// ModelEffortLevels is EffortLevels for a configured provider entry,
// without building a client (no key needed).
func ModelEffortLevels(apiType config.ApiType, baseURL, model string) []string {
	if apiType != config.ApiTypeOpenai {
		return nil
	}
	protocol := protocolChat
	if config.IsOpencodeEndpoint(strings.TrimRight(strings.TrimSpace(baseURL), "/")) {
		protocol = opencodeProtocol(model)
	}
	return EffortLevels(protocol, model)
}
