// thinking.go keeps a model's reasoning so it can be sent back with the
// assistant message it belongs to.
//
// Providers hand reasoning back in three forms: a readable summary, an
// encrypted blob only the issuing model can read (Copilot reasoning_opaque,
// Responses encrypted_content, Anthropic thinking signatures), or both.
// Dropping them makes the model re-plan from scratch on every tool round,
// so Paladin stores whatever arrived and replays it. Encrypted state is
// replayed only to the same provider and model that issued it: another
// model rejects it (HTTP 400) or ignores it. After a model switch the
// readable text goes back inside <prior_reasoning> tags instead.

package inference

import (
	"encoding/json"
	"errors"
	"log"
	"sort"
	"strings"
	"sync"
)

// Wire formats a Thinking can hold, named for the field the provider
// itself used. Replay echoes the same field, so an endpoint is never sent
// a key it did not emit.
const (
	thinkingCopilotChat = "copilot-chat"    // reasoning_text + reasoning_opaque
	thinkingResponses   = "responses"       // Responses API reasoning items
	thinkingBlocks      = "thinking_blocks" // LiteLLM's signed Anthropic blocks
	thinkingContent     = "reasoning_content"
)

// Thinking is one assistant message's reasoning.
type Thinking struct {
	// Text is the readable reasoning (a summary for most models).
	Text string `json:"text,omitempty"`
	// Provider and Model name who issued Format's encrypted state.
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	// Format says which of Opaque and Raw is set; empty means Text only.
	Format string `json:"format,omitempty"`
	// Opaque is Copilot's reasoning_opaque.
	Opaque string `json:"opaque,omitempty"`
	// Raw is a JSON array: Responses reasoning items or thinking blocks.
	Raw json.RawMessage `json:"raw,omitempty"`
}

// Empty reports whether t carries nothing worth storing.
func (t *Thinking) Empty() bool {
	return t == nil || (t.Text == "" && t.Opaque == "" && len(t.Raw) == 0)
}

// priorReasoning renders readable reasoning ahead of an assistant
// message's own text, for a model that cannot read the native state.
func priorReasoning(t *Thinking, content string) string {
	text := strings.TrimSpace(t.Text)
	if text == "" {
		return content
	}
	block := "<prior_reasoning>\n" + text + "\n</prior_reasoning>"
	if content == "" {
		return block
	}
	return block + "\n\n" + content
}

// stampThinking labels freshly captured state with the model that made it.
func (p *OpenAIProvider) stampThinking(t *Thinking) *Thinking {
	if t.Empty() {
		return nil
	}
	t.Provider, t.Model = p.name, p.model
	return t
}

// nativeThinking reports whether t's encrypted state can go back to p.
func (p *OpenAIProvider) nativeThinking(t *Thinking) bool {
	if t == nil || t.Format == "" || t.Provider != p.name || t.Model != p.model {
		return false
	}
	switch t.Format {
	case thinkingCopilotChat:
		return p.isCopilot && p.protocol == protocolChat && t.Opaque != ""
	case thinkingResponses:
		return p.protocol == protocolResponses && len(t.Raw) > 0
	case thinkingMessages:
		return p.isCopilot && p.protocol == protocolAnthropic && len(t.Raw) > 0
	case thinkingBlocks:
		return !p.isCopilot && p.protocol == protocolChat && len(t.Raw) > 0
	case thinkingContent:
		return !p.isCopilot && p.protocol == protocolChat && t.Text != ""
	}
	return false
}

// thinkingPolicy is what the message converters apply to stored reasoning.
// A nil policy leaves reasoning out entirely (OpenCode keeps its own
// handling). Otherwise:
//   - native says whether a message's encrypted state belongs to the
//     receiving model; only then is it sent back;
//   - historicalOK says whether that state still goes back for a prompt the
//     user has already moved past (VS Code replays Responses and Messages
//     reasoning from earlier prompts, but Chat Completions only within the
//     current prompt);
//   - readable says whether reasoning that is not native goes back as
//     <prior_reasoning> text. Copilot never takes it: VS Code drops a
//     different model's reasoning, and Sonnet filters long transcripts of it.
type thinkingPolicy struct {
	native       func(*Thinking) bool
	historicalOK bool
	readable     bool
}

func (p *OpenAIProvider) thinkingReplay() *thinkingPolicy {
	if p.isOpencode {
		return nil
	}
	return &thinkingPolicy{
		native:       p.nativeThinking,
		historicalOK: p.historicalThinkingOK(),
		readable:     !p.isCopilot && !readableBlocked(p),
	}
}

// historicalThinkingOK says whether reasoning from earlier prompts still goes
// back. VS Code replays it on Responses, and on Messages only for adaptive
// models (budget-mode Claude answers HTTP 400); on Chat Completions it
// replays only the current prompt's.
func (p *OpenAIProvider) historicalThinkingOK() bool {
	if !p.isCopilot {
		return true
	}
	switch p.protocol {
	case protocolChat:
		return false
	case protocolAnthropic:
		return p.meta.AdaptiveThinking
	}
	return true
}

// lastUserIndex is the position of the user's latest prompt: assistant
// messages before it belong to earlier prompts.
func lastUserIndex(messages []LlmMessage) int {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return i
		}
	}
	return -1
}

// replayNative reports whether the assistant message at index i (of a
// request whose latest user prompt is at lastUser) sends its encrypted
// reasoning back.
func (pol *thinkingPolicy) replayNative(t *Thinking, i, lastUser int) bool {
	return pol.native(t) && (i > lastUser || pol.historicalOK)
}

// usesReadable reports whether messages carry reasoning that the policy
// would send as readable text.
func (pol *thinkingPolicy) usesReadable(messages []LlmMessage) bool {
	if pol == nil || !pol.readable {
		return false
	}
	for _, m := range messages {
		if m.Role == "assistant" && m.Thinking != nil && strings.TrimSpace(m.Thinking.Text) != "" && !pol.native(m.Thinking) {
			return true
		}
	}
	return false
}

// errReadableFiltered says the gateway answered an empty content_filter
// after readable reasoning from another model was added to the history.
var errReadableFiltered = errors.New("readable reasoning was filtered")

// blockedReadable remembers the models that refused it. Claude Sonnet 5.5
// on Copilot, for one, returns nothing when the history holds a long
// reasoning transcript written by another model. The gateway decides that,
// so it is learned, not guessed.
var blockedReadable sync.Map

func readableKey(p *OpenAIProvider) string { return p.name + "\x00" + p.model }

func readableBlocked(p *OpenAIProvider) bool {
	_, blocked := blockedReadable.Load(readableKey(p))
	return blocked
}

func (p *OpenAIProvider) blockReadable() {
	if _, loaded := blockedReadable.LoadOrStore(readableKey(p), struct{}{}); !loaded {
		log.Printf("inference: %s filtered another model's reasoning for %s; leaving it out from now on", p.name, p.model)
	}
}

// chatReplayFields are the assistant-message keys that carry t back.
func chatReplayFields(t *Thinking) map[string]any {
	switch t.Format {
	case thinkingCopilotChat:
		return map[string]any{"reasoning_text": t.Text, "reasoning_opaque": t.Opaque}
	case thinkingBlocks:
		var blocks []any
		if json.Unmarshal(t.Raw, &blocks) == nil {
			return map[string]any{"thinking_blocks": blocks}
		}
	case thinkingContent:
		return map[string]any{"reasoning_content": t.Text}
	}
	return nil
}

// chatDelta is the reasoning part of a chat message or streamed delta.
type chatDelta struct {
	ReasoningText    string `json:"reasoning_text"`
	ReasoningOpaque  string `json:"reasoning_opaque"`
	ReasoningContent string `json:"reasoning_content"`
	Reasoning        string `json:"reasoning"`
	Blocks           []struct {
		Index     *int   `json:"index"`
		Type      string `json:"type"`
		Thinking  string `json:"thinking"`
		Signature string `json:"signature"`
		Data      string `json:"data"`
	} `json:"thinking_blocks"`
}

type thinkingBlock struct {
	typ, text, signature, data string
}

// chatThinking assembles one chat response's reasoning from its deltas.
type chatThinking struct {
	text, content strings.Builder
	opaque        string
	indexed       map[int]*thinkingBlock
	whole         []thinkingBlock
}

// add folds one message or delta in and returns the readable piece for
// the live display.
func (c *chatThinking) add(raw string) string {
	if !strings.Contains(raw, `"reasoning`) && !strings.Contains(raw, `"thinking_blocks"`) {
		return ""
	}
	var d chatDelta
	if json.Unmarshal([]byte(raw), &d) != nil {
		return ""
	}
	if d.ReasoningText != "" {
		c.text.WriteString(d.ReasoningText)
	}
	// A response can carry several opaque values; the latest is the one
	// that covers everything before it.
	if d.ReasoningOpaque != "" {
		c.opaque = d.ReasoningOpaque
	}
	if d.ReasoningContent != "" {
		c.content.WriteString(d.ReasoningContent)
	}
	for _, b := range d.Blocks {
		if b.Index == nil {
			c.whole = append(c.whole, thinkingBlock{b.Type, b.Thinking, b.Signature, b.Data})
			continue
		}
		if c.indexed == nil {
			c.indexed = map[int]*thinkingBlock{}
		}
		blk := c.indexed[*b.Index]
		if blk == nil {
			blk = &thinkingBlock{typ: b.Type}
			c.indexed[*b.Index] = blk
		}
		blk.text += b.Thinking
		if b.Signature != "" {
			blk.signature = b.Signature
		}
		if b.Data != "" {
			blk.data = b.Data
		}
	}
	switch {
	case d.ReasoningText != "":
		return d.ReasoningText
	case d.ReasoningContent != "":
		return d.ReasoningContent
	}
	return d.Reasoning
}

// blocksJSON is the signed blocks in wire order, as a JSON array.
func (c *chatThinking) blocksJSON() json.RawMessage {
	blocks := append([]thinkingBlock(nil), c.whole...)
	keys := make([]int, 0, len(c.indexed))
	for k := range c.indexed {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	for _, k := range keys {
		blocks = append(blocks, *c.indexed[k])
	}
	out := make([]map[string]any, 0, len(blocks))
	for _, b := range blocks {
		switch {
		case b.typ == "redacted_thinking" && b.data != "":
			out = append(out, map[string]any{"type": b.typ, "data": b.data})
		case b.signature != "":
			// An unsigned block is rejected on replay, so it is not kept.
			out = append(out, map[string]any{"type": "thinking", "thinking": b.text, "signature": b.signature})
		}
	}
	if len(out) == 0 {
		return nil
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return nil
	}
	return raw
}

// result is the response's Thinking, in the form p's endpoint speaks.
func (c *chatThinking) result(p *OpenAIProvider) *Thinking {
	t := &Thinking{Text: c.text.String()}
	if t.Text == "" {
		t.Text = c.content.String()
	}
	switch {
	case p.isCopilot && c.opaque != "":
		t.Format, t.Opaque = thinkingCopilotChat, c.opaque
	case !p.isCopilot && c.blocksJSON() != nil:
		t.Format, t.Raw = thinkingBlocks, c.blocksJSON()
	case !p.isCopilot && c.content.Len() > 0:
		t.Format = thinkingContent
	}
	return p.stampThinking(t)
}
