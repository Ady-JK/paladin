// anthropic.go implements the Anthropic Messages
// dialect used by OpenCode Go for Claude models.

package inference

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

const (
	anthropicVersion  = "2023-06-01"
	anthropicMaxToken = 8192
)

type anthropicBlock struct {
	Type      string                `json:"type"`
	Text      string                `json:"text,omitempty"`
	ID        string                `json:"id,omitempty"`
	Name      string                `json:"name,omitempty"`
	Input     any                   `json:"input,omitempty"`
	ToolUseID string                `json:"tool_use_id,omitempty"`
	Content   string                `json:"content,omitempty"`
	IsError   bool                  `json:"is_error,omitempty"`
	Source    *anthropicImageSource `json:"source,omitempty"`
	// Thinking, Signature and Data carry a thinking block (Data is the body
	// of a redacted_thinking block); CacheControl marks a prompt-cache
	// breakpoint. Thinking is a pointer so an empty thinking text is still
	// sent, which the API requires.
	Thinking     *string         `json:"thinking,omitempty"`
	Signature    string          `json:"signature,omitempty"`
	Data         string          `json:"data,omitempty"`
	CacheControl *anthropicCache `json:"cache_control,omitempty"`
}

type anthropicImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type anthropicMessage struct {
	Role    string           `json:"role"`
	Content []anthropicBlock `json:"content"`
}

type anthropicTool struct {
	Name         string          `json:"name"`
	Description  string          `json:"description,omitempty"`
	InputSchema  map[string]any  `json:"input_schema"`
	CacheControl *anthropicCache `json:"cache_control,omitempty"`
}

type anthropicRequest struct {
	Model     string `json:"model"`
	MaxTokens int    `json:"max_tokens"`
	// System is a string for OpenCode and a list of text blocks for Copilot.
	System       any                    `json:"system,omitempty"`
	Messages     []anthropicMessage     `json:"messages"`
	Tools        []anthropicTool        `json:"tools,omitempty"`
	Stream       bool                   `json:"stream,omitempty"`
	Thinking     *anthropicThinking     `json:"thinking,omitempty"`
	OutputConfig *anthropicOutputConfig `json:"output_config,omitempty"`
}

func toolInput(arguments string) any {
	var input any
	if json.Unmarshal([]byte(arguments), &input) == nil {
		if _, ok := input.(map[string]any); ok {
			return input
		}
	}
	return map[string]any{}
}

func toAnthropicRequest(system string, messages []LlmMessage, tools []ToolDefinition) anthropicRequest {
	return buildAnthropicRequest(system, messages, tools, false)
}

func (p *OpenAIProvider) anthropicRequest(system string, messages []LlmMessage, tools []ToolDefinition) anthropicRequest {
	if p.isCopilot {
		return p.copilotMessagesRequest(system, messages, tools)
	}
	return buildAnthropicRequest(system, messages, tools, false)
}

func anthropicUserBlocks(message LlmMessage, includeImage bool) []anthropicBlock {
	blocks := []anthropicBlock{{Type: "text", Text: message.Content}}
	if !includeImage || message.Image == nil {
		return blocks
	}
	url, err := message.Image.DataURL()
	if err != nil {
		return append(blocks, anthropicBlock{Type: "text", Text: "Image unavailable: " + err.Error()})
	}
	_, data, _ := strings.Cut(url, ",")
	return append(blocks, anthropicBlock{
		Type: "image", Source: &anthropicImageSource{Type: "base64", MediaType: message.Image.MIMEType, Data: data},
	})
}

func buildAnthropicRequest(system string, messages []LlmMessage, tools []ToolDefinition, includeUserImages bool) anthropicRequest {
	req := anthropicRequest{
		MaxTokens: anthropicMaxToken,
		Messages:  []anthropicMessage{},
	}
	if system != "" {
		req.System = system
	}
	var pendingToolResults []anthropicBlock
	flushTools := func() {
		if len(pendingToolResults) == 0 {
			return
		}
		req.Messages = append(req.Messages, anthropicMessage{Role: "user", Content: pendingToolResults})
		pendingToolResults = nil
	}
	for _, m := range messages {
		switch m.Role {
		case "assistant":
			var blocks []anthropicBlock
			if m.Content != "" {
				blocks = append(blocks, anthropicBlock{Type: "text", Text: m.Content})
			}
			for _, c := range m.ToolCalls {
				blocks = append(blocks, anthropicBlock{
					Type: "tool_use", ID: c.ID, Name: c.Name, Input: toolInput(c.Arguments),
				})
			}
			if len(blocks) > 0 {
				flushTools()
				req.Messages = append(req.Messages, anthropicMessage{Role: "assistant", Content: blocks})
			}
		case "tool":
			id := ""
			if m.ToolCallID != nil {
				id = *m.ToolCallID
			}
			pendingToolResults = append(pendingToolResults, anthropicBlock{
				Type: "tool_result", ToolUseID: id, Content: m.Content,
			})
			if m.Image != nil {
				if url, err := m.Image.DataURL(); err == nil {
					_, data, _ := strings.Cut(url, ",")
					pendingToolResults = append(pendingToolResults, anthropicBlock{
						Type: "image", Source: &anthropicImageSource{Type: "base64", MediaType: m.Image.MIMEType, Data: data},
					})
				} else {
					pendingToolResults = append(pendingToolResults, anthropicBlock{Type: "text", Text: "Image unavailable: " + err.Error()})
				}
			}
		case "system":
			// Instructions live in the top-level system field.
		default:
			blocks := anthropicUserBlocks(m, includeUserImages)
			if len(pendingToolResults) > 0 {
				pendingToolResults = append(pendingToolResults, blocks...)
				flushTools()
				continue
			}
			req.Messages = append(req.Messages, anthropicMessage{
				Role: "user", Content: blocks,
			})
		}
	}
	flushTools()
	for _, t := range tools {
		req.Tools = append(req.Tools, anthropicTool{
			Name: t.Name, Description: t.Description, InputSchema: schemaMap(t.Parameters),
		})
	}
	return req
}

// anthropicUsageBlock is the usage envelope the Messages API sends in
// message_start (input side, on .message.usage) and message_delta
// (output side, on .usage). Field names follow the Anthropic API.
type anthropicUsageBlock struct {
	InputTokens              int64 `json:"input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
}

func (p *OpenAIProvider) messagesURL() string {
	if p.isCopilot {
		return strings.TrimRight(p.baseURL, "/") + "/v1/messages"
	}
	return p.anthropicURL()
}

func (p *OpenAIProvider) completeAnthropic(ctx context.Context, system string, messages []LlmMessage, tools []ToolDefinition, sessionID string) (ToolResponse, error) {
	body := p.anthropicRequest(system, messages, tools)
	body.Model = p.model
	if p.isCopilot && body.MaxTokens > messagesBufferedMaxTokens {
		// Long requests must stream; a buffered call stays under that size.
		body.MaxTokens = messagesBufferedMaxTokens
		if body.Thinking != nil && body.Thinking.BudgetTokens >= body.MaxTokens {
			body.Thinking.BudgetTokens = body.MaxTokens - 1
		}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return ToolResponse{}, RequestError(fmt.Sprintf("%s: anthropic: encode: %v", p.name, err))
	}
	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.messagesURL(), bytes.NewReader(raw))
	if err != nil {
		return ToolResponse{}, RequestError(fmt.Sprintf("%s: anthropic: %v", p.name, err))
	}
	p.anthropicHeaders(ctx, req, messages, sessionID, false)
	resp, err := providerHTTPClient.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return ToolResponse{}, CancelledError()
		}
		return ToolResponse{}, RequestError(fmt.Sprintf("%s: anthropic: %v", p.name, err))
	}
	defer resp.Body.Close()
	data, err := readBody(resp.Body, maxResponseBody)
	if err != nil {
		return ToolResponse{}, RequestError(fmt.Sprintf("%s: anthropic: %v", p.name, err))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ToolResponse{}, statusErrorFrom(p.name, "anthropic", resp, string(data))
	}
	var wire struct {
		Content []struct {
			Type      string          `json:"type"`
			Text      string          `json:"text"`
			ID        string          `json:"id"`
			Name      string          `json:"name"`
			Input     json.RawMessage `json:"input"`
			Thinking  string          `json:"thinking"`
			Signature string          `json:"signature"`
			Data      string          `json:"data"`
		} `json:"content"`
		Usage anthropicUsageBlock `json:"usage"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return ToolResponse{}, RequestError(fmt.Sprintf("%s: anthropic: decode: %v", p.name, err))
	}
	if wire.Error != nil && wire.Error.Message != "" {
		return ToolResponse{}, RequestError(fmt.Sprintf("%s: anthropic: %s", p.name, wire.Error.Message))
	}
	var out ToolResponse
	var text strings.Builder
	var thinking messagesThinkingAcc
	for i, block := range wire.Content {
		switch block.Type {
		case "thinking", "redacted_thinking":
			if p.isCopilot {
				thinking.start(i, block.Type, block.Thinking, block.Data)
				thinking.addSignature(i, block.Signature)
			}
		case "text":
			text.WriteString(block.Text)
		case "tool_use":
			args := string(block.Input)
			if strings.TrimSpace(args) == "" {
				args = "{}"
			}
			out.ToolCalls = append(out.ToolCalls, ToolCall{ID: block.ID, Name: block.Name, Arguments: args})
		}
	}
	out.Content = text.String()
	if p.isCopilot {
		out.Reasoning = thinking.display.String()
		out.Thinking = thinking.result(p)
	}
	if u := anthropicUsage(wire.Usage); u != nil {
		out.Usage = p.estimateCopilotUsage(u)
	}
	return out, nil
}

// merge folds a streamed usage report into b. The Messages API reports usage
// twice per call, on message_start and again on message_delta, and the counts
// are cumulative: message_delta may repeat the input side and carries the
// final output total. Each field therefore keeps its largest value; adding the
// two reports counted every call's input and cache twice.
func (b *anthropicUsageBlock) merge(o anthropicUsageBlock) {
	b.InputTokens = max(b.InputTokens, o.InputTokens)
	b.CacheCreationInputTokens = max(b.CacheCreationInputTokens, o.CacheCreationInputTokens)
	b.CacheReadInputTokens = max(b.CacheReadInputTokens, o.CacheReadInputTokens)
	b.OutputTokens = max(b.OutputTokens, o.OutputTokens)
}

// anthropicUsage maps one call's usage block onto Usage. Anthropic's
// input_tokens leaves cached input out, so the call's prompt is the sum of the
// three input fields: the same meaning prompt_tokens has everywhere else, which
// keeps the context fill, the cache share and the totals comparable.
func anthropicUsage(b anthropicUsageBlock) *Usage {
	out := &Usage{
		PromptTokens:     b.InputTokens + b.CacheReadInputTokens + b.CacheCreationInputTokens,
		CompletionTokens: b.OutputTokens,
		CacheReadTokens:  b.CacheReadInputTokens,
		CacheWriteTokens: b.CacheCreationInputTokens,
	}
	out.TotalTokens = out.PromptTokens + out.CompletionTokens
	if *out == (Usage{}) {
		return nil
	}
	out.CacheReadKnown = true
	out.CacheWriteKnown = true
	return out
}

func (p *OpenAIProvider) anthropicHeaders(ctx context.Context, req *http.Request, messages []LlmMessage, sessionID string, stream bool) {
	req.Header.Set("Content-Type", "application/json")
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	} else {
		req.Header.Set("Accept", "application/json")
	}
	req.Header.Set("anthropic-version", anthropicVersion)
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	if p.isCopilot {
		req.Header.Set("Copilot-Integration-Id", "vscode-chat")
		req.Header.Set("Editor-Version", "vscode/1.95.0")
		req.Header.Set("Editor-Plugin-Version", "copilot-chat/0.26.7")
		req.Header.Set("User-Agent", "GitHubCopilotChat/0.26.7")
		for key, value := range p.copilotRequestHeaders(ctx, messages) {
			req.Header.Set(key, value)
		}
		if beta := p.messagesBetaHeader(); beta != "" {
			req.Header.Set("anthropic-beta", beta)
		}
	}
	p.setSessionHeaders(req, sessionID)
}

type anthropicCallAcc struct {
	id        string
	name      string
	arguments strings.Builder
	index     int
}

func (p *OpenAIProvider) streamAnthropic(ctx context.Context, system string, messages []LlmMessage, tools []ToolDefinition, sessionID string, tx chan<- Chunk, done <-chan struct{}) error {
	parent := ctx
	tctx, idle, cancel := watchIdle(ctx)
	defer cancel()
	body := p.anthropicRequest(system, messages, tools)
	body.Model = p.model
	body.Stream = true
	raw, err := json.Marshal(body)
	if err != nil {
		return RequestError(fmt.Sprintf("%s: anthropic: encode: %v", p.name, err))
	}
	req, err := http.NewRequestWithContext(tctx, http.MethodPost, p.messagesURL(), bytes.NewReader(raw))
	if err != nil {
		return RequestError(fmt.Sprintf("%s: anthropic: %v", p.name, err))
	}
	p.anthropicHeaders(ctx, req, messages, sessionID, true)
	resp, err := providerHTTPClient.Do(req)
	if err != nil {
		if parent.Err() != nil {
			return CancelledError()
		}
		if idle.Fired() {
			return idleError(p.name, "anthropic")
		}
		return RequestError(fmt.Sprintf("%s: anthropic: %v", p.name, err))
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return statusErrorFrom(p.name, "anthropic", resp, readErrorBody(resp.Body))
	}

	send := func(c Chunk) error {
		select {
		case tx <- c:
			return nil
		case <-done:
			return errClientGone
		case <-parent.Done():
			return CancelledError()
		}
	}
	accs := map[int]*anthropicCallAcc{}
	var streamBlock anthropicUsageBlock // cumulative: merged, never summed
	var thinking messagesThinkingAcc
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		idle.touch()
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var ev struct {
			Type    string `json:"type"`
			Index   int    `json:"index"`
			Message *struct {
				Usage anthropicUsageBlock `json:"usage"`
			} `json:"message"`
			Usage        *anthropicUsageBlock `json:"usage"`
			ContentBlock *struct {
				Type     string `json:"type"`
				ID       string `json:"id"`
				Name     string `json:"name"`
				Thinking string `json:"thinking"`
				Data     string `json:"data"`
			} `json:"content_block"`
			Delta *struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
				Thinking    string `json:"thinking"`
				Signature   string `json:"signature"`
				StopReason  string `json:"stop_reason"`
			} `json:"delta"`
			Error *struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			continue
		}
		switch ev.Type {
		case "message_start":
			// Input-side usage rides on message_start.message.usage.
			if ev.Message != nil {
				streamBlock.merge(ev.Message.Usage)
			}
		case "message_delta":
			// Output tokens arrive here.
			if ev.Usage != nil {
				streamBlock.merge(*ev.Usage)
			}
			if p.isCopilot && ev.Delta != nil && ev.Delta.StopReason == "refusal" {
				return RequestError(fmt.Sprintf("%s: anthropic: the model refused to answer (stop_reason refusal)", p.name))
			}
		case "content_block_start":
			if p.isCopilot && ev.ContentBlock != nil && (ev.ContentBlock.Type == "thinking" || ev.ContentBlock.Type == "redacted_thinking") {
				thinking.start(ev.Index, ev.ContentBlock.Type, ev.ContentBlock.Thinking, ev.ContentBlock.Data)
			}
			if ev.ContentBlock != nil && ev.ContentBlock.Type == "tool_use" {
				acc := &anthropicCallAcc{id: ev.ContentBlock.ID, name: ev.ContentBlock.Name, index: ev.Index}
				accs[ev.Index] = acc
				if err := send(Chunk{ToolNotice: &ToolCall{ID: acc.id, Name: acc.name, Arguments: "{}"}}); err != nil {
					return CancelledError()
				}
			}
		case "content_block_delta":
			if ev.Delta == nil {
				continue
			}
			switch ev.Delta.Type {
			case "text_delta":
				if ev.Delta.Text != "" {
					if err := send(TextChunk(ev.Delta.Text)); err != nil {
						return CancelledError()
					}
				}
			case "input_json_delta":
				if acc := accs[ev.Index]; acc != nil {
					acc.arguments.WriteString(ev.Delta.PartialJSON)
				}
			case "thinking_delta":
				if p.isCopilot && ev.Delta.Thinking != "" {
					thinking.addText(ev.Index, ev.Delta.Thinking)
					if err := send(Chunk{Reasoning: ev.Delta.Thinking}); err != nil {
						return CancelledError()
					}
				}
			case "signature_delta":
				if p.isCopilot {
					thinking.addSignature(ev.Index, ev.Delta.Signature)
				}
			}
		case "message_stop":
			if p.isCopilot {
				if th := thinking.result(p); th != nil {
					if err := send(Chunk{Thinking: th}); err != nil {
						return CancelledError()
					}
				}
			}
			if calls := anthropicCalls(accs); len(calls) > 0 {
				if err := send(Chunk{ToolCalls: calls}); err != nil {
					return CancelledError()
				}
			}
			if u := anthropicUsage(streamBlock); u != nil {
				if err := send(Chunk{Usage: p.estimateCopilotUsage(u)}); err != nil {
					return CancelledError()
				}
			}
			return nil
		case "error":
			msg := "response error"
			if ev.Error != nil {
				if ev.Error.Message != "" {
					msg = ev.Error.Message
				} else if ev.Error.Type != "" {
					msg = ev.Error.Type
				}
			}
			return RequestError(fmt.Sprintf("%s: anthropic: %s", p.name, msg))
		}
		select {
		case <-done:
			return CancelledError()
		case <-parent.Done():
			return CancelledError()
		default:
		}
	}
	if parent.Err() == nil && idle.Fired() {
		return idleError(p.name, "anthropic")
	}
	if err := scanner.Err(); err != nil {
		if parent.Err() != nil {
			return CancelledError()
		}
		return RequestError(fmt.Sprintf("%s: anthropic: %v", p.name, err))
	}
	if calls := anthropicCalls(accs); len(calls) > 0 {
		select {
		case tx <- Chunk{ToolCalls: calls}:
		case <-done:
			return CancelledError()
		case <-parent.Done():
			return CancelledError()
		}
	}
	if u := anthropicUsage(streamBlock); u != nil {
		select {
		case tx <- Chunk{Usage: p.estimateCopilotUsage(u)}:
		case <-done:
			return CancelledError()
		case <-parent.Done():
			return CancelledError()
		}
	}
	return nil
}

func anthropicCalls(accs map[int]*anthropicCallAcc) []ToolCall {
	if len(accs) == 0 {
		return nil
	}
	indexes := make([]int, 0, len(accs))
	for i := range accs {
		indexes = append(indexes, i)
	}
	sort.Ints(indexes)
	out := make([]ToolCall, 0, len(indexes))
	for _, i := range indexes {
		acc := accs[i]
		out = append(out, ToolCall{ID: acc.id, Name: acc.name, Arguments: acc.arguments.String()})
	}
	return out
}
