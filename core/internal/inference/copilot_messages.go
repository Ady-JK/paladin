// copilot_messages.go builds Copilot's /v1/messages requests the way the
// VS Code Copilot extension does: adaptive or budgeted thinking, signed
// thinking blocks replayed ahead of the tool calls they led to, at most four
// prompt-cache markers, sanitised tool ids, and no request that ends on an
// assistant message. OpenCode's Anthropic gateway keeps the plain builder in
// anthropic.go.

package inference

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/Ady-JK/paladin/core/internal/media"
)

const thinkingMessages = "messages" // signed thinking / redacted_thinking blocks

const (
	// messagesBufferedMaxTokens keeps a non-streamed call under the size
	// Anthropic requires streaming for.
	messagesBufferedMaxTokens = 16384
	// messagesThinkingBase is the thinking budget VS Code aims for before
	// clamping to the model's range.
	messagesThinkingBase = 16000
	continuePrompt       = "Please continue."
)

type anthropicCache struct {
	Type string `json:"type"`
}

type anthropicThinking struct {
	Type         string `json:"type"`
	Display      string `json:"display,omitempty"`
	BudgetTokens int    `json:"budget_tokens,omitempty"`
}

type anthropicOutputConfig struct {
	Effort string `json:"effort"`
}

var ephemeral = &anthropicCache{Type: "ephemeral"}

var toolIDUnsafe = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

// toolIDMap rewrites tool ids Anthropic would reject, keeping every
// tool_use paired with its tool_result and every rewritten id unique.
type toolIDMap struct {
	mapped map[string]string
	used   map[string]bool
}

func newToolIDMap() *toolIDMap {
	return &toolIDMap{mapped: map[string]string{}, used: map[string]bool{}}
}

func (m *toolIDMap) id(raw string) string {
	if id, ok := m.mapped[raw]; ok {
		return id
	}
	id := toolIDUnsafe.ReplaceAllString(raw, "_")
	if id != raw {
		base := id
		for n := 1; m.used[id]; n++ {
			id = base + "_" + strconv.Itoa(n)
		}
	}
	m.mapped[raw], m.used[id] = id, true
	return id
}

func isThinkingBlock(b anthropicBlock) bool {
	return b.Type == "thinking" || b.Type == "redacted_thinking"
}

// signedThinkingBlocks are t's stored blocks, signed ones only: an unsigned block
// is rejected on replay.
func signedThinkingBlocks(t *Thinking) []anthropicBlock {
	var stored []struct {
		Type      string `json:"type"`
		Thinking  string `json:"thinking"`
		Signature string `json:"signature"`
		Data      string `json:"data"`
	}
	if json.Unmarshal(t.Raw, &stored) != nil {
		return nil
	}
	var out []anthropicBlock
	for _, b := range stored {
		switch {
		case b.Type == "redacted_thinking" && b.Data != "":
			out = append(out, anthropicBlock{Type: "redacted_thinking", Data: b.Data})
		case b.Type == "thinking" && b.Signature != "":
			text := b.Thinking
			out = append(out, anthropicBlock{Type: "thinking", Thinking: &text, Signature: b.Signature})
		}
	}
	return out
}

func (p *OpenAIProvider) messagesMaxTokens() int {
	if p.meta.MaxOutput > 0 {
		return int(p.meta.MaxOutput)
	}
	return anthropicMaxToken
}

// copilotMessagesRequest converts the conversation into a Messages request.
func (p *OpenAIProvider) copilotMessagesRequest(system string, messages []LlmMessage, tools []ToolDefinition) anthropicRequest {
	req := anthropicRequest{MaxTokens: p.messagesMaxTokens(), Messages: []anthropicMessage{}}
	if strings.TrimSpace(system) != "" {
		req.System = []anthropicBlock{{Type: "text", Text: system}}
	}
	pol := p.thinkingReplay()
	lastUser := lastUserIndex(messages)
	ids := newToolIDMap()
	var out []anthropicMessage
	var pendingTools []anthropicBlock
	flushTools := func() {
		if len(pendingTools) > 0 {
			out = append(out, anthropicMessage{Role: "user", Content: pendingTools})
			pendingTools = nil
		}
	}
	firstAfterPrompt := -1 // the first assistant message after the user's latest prompt
	replayed := false      // and whether its thinking went back
	for i, m := range messages {
		switch m.Role {
		case "assistant":
			var blocks []anthropicBlock
			sent := false
			if pol != nil && m.Thinking != nil && pol.replayNative(m.Thinking, i, lastUser) {
				if tb := signedThinkingBlocks(m.Thinking); len(tb) > 0 {
					blocks, sent = append(blocks, tb...), true
				}
			}
			if strings.TrimSpace(m.Content) != "" {
				blocks = append(blocks, anthropicBlock{Type: "text", Text: m.Content})
			}
			for _, c := range m.ToolCalls {
				blocks = append(blocks, anthropicBlock{Type: "tool_use", ID: ids.id(c.ID), Name: c.Name, Input: toolInput(c.Arguments)})
			}
			if i > lastUser && firstAfterPrompt < 0 {
				firstAfterPrompt, replayed = i, sent
			}
			if len(blocks) > 0 {
				flushTools()
				out = append(out, anthropicMessage{Role: "assistant", Content: blocks})
			}
		case "tool":
			id := ""
			if m.ToolCallID != nil {
				id = ids.id(*m.ToolCallID)
			}
			pendingTools = append(pendingTools, anthropicBlock{Type: "tool_result", ToolUseID: id, Content: m.Content})
			if m.Image != nil {
				pendingTools = append(pendingTools, imageBlocks(m.Image)...)
			}
		case "system":
			// Instructions live in the system field.
		default:
			var blocks []anthropicBlock
			if strings.TrimSpace(m.Content) != "" {
				blocks = append(blocks, anthropicBlock{Type: "text", Text: m.Content})
			}
			if m.Image != nil {
				blocks = append(blocks, imageBlocks(m.Image)...)
			}
			if len(blocks) == 0 {
				continue
			}
			if len(pendingTools) > 0 {
				pendingTools = append(pendingTools, blocks...)
				flushTools()
				continue
			}
			out = append(out, anthropicMessage{Role: "user", Content: blocks})
		}
	}
	flushTools()
	out = mergeNeighbours(out)
	if n := len(out); n > 0 && out[n-1].Role == "assistant" {
		// A request may not end on an assistant message (that is a prefill,
		// which current models reject).
		out = append(out, anthropicMessage{Role: "user", Content: []anthropicBlock{{Type: "text", Text: continuePrompt}}})
	}
	req.Messages = out

	for _, t := range tools {
		req.Tools = append(req.Tools, anthropicTool{Name: t.Name, Description: t.Description, InputSchema: schemaMap(t.Parameters)})
	}
	p.applyMessagesThinking(&req, firstAfterPrompt >= 0 && !replayed)
	addMessagesCacheControl(&req)
	return req
}

// imageBlocks is img as an Anthropic image block, or a text note when its
// bytes cannot be read.
func imageBlocks(img *media.Image) []anthropicBlock {
	url, err := img.DataURL()
	if err != nil {
		return []anthropicBlock{{Type: "text", Text: "Image unavailable: " + err.Error()}}
	}
	_, data, _ := strings.Cut(url, ",")
	return []anthropicBlock{{Type: "image", Source: &anthropicImageSource{Type: "base64", MediaType: img.MIMEType, Data: data}}}
}

// mergeNeighbours joins consecutive messages of one role, since Messages
// wants strict alternation. Of two assistant messages only one response's
// thinking survives, hoisted to the front: the newest that made tool calls,
// else the older one that did, else none.
func mergeNeighbours(in []anthropicMessage) []anthropicMessage {
	var out []anthropicMessage
	for _, m := range in {
		n := len(out)
		if n == 0 || out[n-1].Role != m.Role {
			out = append(out, m)
			continue
		}
		prev := out[n-1]
		if m.Role == "user" {
			out[n-1].Content = append(append([]anthropicBlock(nil), prev.Content...), m.Content...)
			continue
		}
		var thinking, rest []anthropicBlock
		keepFrom := m
		if !hasToolUse(m) && hasToolUse(prev) {
			keepFrom = prev
		}
		for _, b := range keepFrom.Content {
			if isThinkingBlock(b) {
				thinking = append(thinking, b)
			}
		}
		for _, src := range []anthropicMessage{prev, m} {
			for _, b := range src.Content {
				if !isThinkingBlock(b) {
					rest = append(rest, b)
				}
			}
		}
		out[n-1].Content = append(append([]anthropicBlock(nil), thinking...), rest...)
	}
	return out
}

func hasToolUse(m anthropicMessage) bool {
	for _, b := range m.Content {
		if b.Type == "tool_use" {
			return true
		}
	}
	return false
}

// applyMessagesThinking sets thinking and effort. Thinking needs a model
// that advertises it, and it must stay off when the prompt's first tool
// round was sent without a thinking block: a tool loop that began without
// thinking cannot continue under it.
func (p *OpenAIProvider) applyMessagesThinking(req *anthropicRequest, unthoughtContinuation bool) {
	if unthoughtContinuation {
		return
	}
	switch {
	case p.meta.AdaptiveThinking:
		req.Thinking = &anthropicThinking{Type: "adaptive", Display: "summarized"}
	case p.meta.MaxThinkingBudget > 0 && p.meta.MinThinkingBudget > 0:
		budget := int64(messagesThinkingBase)
		if p.meta.MinThinkingBudget > budget {
			budget = p.meta.MinThinkingBudget
		}
		if p.meta.MaxThinkingBudget < budget {
			budget = p.meta.MaxThinkingBudget
		}
		if limit := int64(req.MaxTokens - 1); limit < budget {
			budget = limit
		}
		req.Thinking = &anthropicThinking{Type: "enabled", BudgetTokens: int(budget)}
	default:
		return
	}
	levels := p.meta.ThinkingLevels
	effort := p.effort
	switch {
	case effort != "":
	case len(levels) > 0:
		effort = levels[len(levels)/2]
		for _, l := range levels {
			if l == EffortMedium {
				effort = l
			}
		}
	case p.meta.AdaptiveThinking && !p.meta.ThinkingKnown:
		effort = EffortHigh
	}
	if effort != "" && !(p.meta.ThinkingKnown && len(levels) == 0) {
		req.OutputConfig = &anthropicOutputConfig{Effort: effort}
	}
}

// addMessagesCacheControl places the prompt-cache markers: the last tool, the
// last system block, and the last cacheable block of the two newest messages
// that hold anything besides thinking. That is four at most, the API limit,
// and they move forward every round because the request is rebuilt each time.
func addMessagesCacheControl(req *anthropicRequest) {
	if n := len(req.Tools); n > 0 {
		req.Tools[n-1].CacheControl = ephemeral
	}
	if blocks, ok := req.System.([]anthropicBlock); ok && len(blocks) > 0 {
		blocks[len(blocks)-1].CacheControl = ephemeral
	}
	marked := 0
	for i := len(req.Messages) - 1; i >= 0 && marked < 2; i-- {
		content := req.Messages[i].Content
		for j := len(content) - 1; j >= 0; j-- {
			if !isThinkingBlock(content[j]) {
				content[j].CacheControl = ephemeral
				marked++
				break
			}
		}
	}
}

// messagesBetaHeader is the anthropic-beta value for a Copilot model:
// interleaved thinking, for models that think on a fixed budget.
func (p *OpenAIProvider) messagesBetaHeader() string {
	if p.meta.AdaptiveThinking {
		return ""
	}
	return "interleaved-thinking-2025-05-14"
}

// messagesThinkingAcc assembles one response's thinking blocks.
type messagesThinkingAcc struct {
	blocks  map[int]*thinkingBlock
	order   []int
	display strings.Builder
}

func (a *messagesThinkingAcc) start(index int, typ, text, data string) {
	if a.blocks == nil {
		a.blocks = map[int]*thinkingBlock{}
	}
	a.blocks[index] = &thinkingBlock{typ: typ, text: text, data: data}
	a.order = append(a.order, index)
	a.display.WriteString(text)
}

func (a *messagesThinkingAcc) addText(index int, text string) {
	if b := a.blocks[index]; b != nil {
		b.text += text
		a.display.WriteString(text)
	}
}

func (a *messagesThinkingAcc) addSignature(index int, sig string) {
	if b := a.blocks[index]; b != nil {
		b.signature += sig
	}
}

// result is the thinking to keep, or nil when nothing signed arrived.
func (a *messagesThinkingAcc) result(p *OpenAIProvider) *Thinking {
	var out []map[string]any
	for _, i := range a.order {
		b := a.blocks[i]
		switch {
		case b.typ == "redacted_thinking" && b.data != "":
			out = append(out, map[string]any{"type": "redacted_thinking", "data": b.data})
		case b.typ == "thinking" && b.signature != "":
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
	return p.stampThinking(&Thinking{Format: thinkingMessages, Raw: raw})
}
