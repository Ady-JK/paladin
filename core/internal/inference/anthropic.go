// Package inference: anthropic.go implements the Anthropic Messages
// dialect used by OpenCode Go for Claude models.
package inference

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

const (
	anthropicVersion  = "2023-06-01"
	anthropicMaxToken = 8192
)

type anthropicBlock struct {
	Type      string `json:"type"`
	Text      string `json:"text,omitempty"`
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Input     any    `json:"input,omitempty"`
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
}

type anthropicMessage struct {
	Role    string           `json:"role"`
	Content []anthropicBlock `json:"content"`
}

type anthropicTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema"`
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
	Tools     []anthropicTool    `json:"tools,omitempty"`
	Stream    bool               `json:"stream,omitempty"`
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
	req := anthropicRequest{
		MaxTokens: anthropicMaxToken,
		System:    system,
		Messages:  []anthropicMessage{},
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
		case "system":
			// Instructions live in the top-level system field.
		default:
			if len(pendingToolResults) > 0 {
				pendingToolResults = append(pendingToolResults, anthropicBlock{Type: "text", Text: m.Content})
				flushTools()
				continue
			}
			req.Messages = append(req.Messages, anthropicMessage{
				Role: "user", Content: []anthropicBlock{{Type: "text", Text: m.Content}},
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

func (p *OpenAIProvider) completeAnthropic(ctx context.Context, system string, messages []LlmMessage, tools []ToolDefinition, sessionID string) (ToolResponse, error) {
	body := toAnthropicRequest(system, messages, tools)
	body.Model = p.model
	raw, err := json.Marshal(body)
	if err != nil {
		return ToolResponse{}, RequestError(fmt.Sprintf("%s: anthropic: encode: %v", p.name, err))
	}
	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.anthropicURL(), bytes.NewReader(raw))
	if err != nil {
		return ToolResponse{}, RequestError(fmt.Sprintf("%s: anthropic: %v", p.name, err))
	}
	p.anthropicHeaders(req, sessionID, false)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return ToolResponse{}, CancelledError()
		}
		return ToolResponse{}, RequestError(fmt.Sprintf("%s: anthropic: %v", p.name, err))
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return ToolResponse{}, RequestError(fmt.Sprintf("%s: anthropic: %v", p.name, err))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ToolResponse{}, p.statusError("anthropic", resp.StatusCode, string(data))
	}
	var wire struct {
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
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
	for _, block := range wire.Content {
		switch block.Type {
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
	return out, nil
}

func (p *OpenAIProvider) anthropicHeaders(req *http.Request, sessionID string, stream bool) {
	req.Header.Set("Content-Type", "application/json")
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	} else {
		req.Header.Set("Accept", "application/json")
	}
	req.Header.Set("anthropic-version", anthropicVersion)
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
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
	tctx, cancel := context.WithTimeout(ctx, streamTimeout)
	defer cancel()
	body := toAnthropicRequest(system, messages, tools)
	body.Model = p.model
	body.Stream = true
	raw, err := json.Marshal(body)
	if err != nil {
		return RequestError(fmt.Sprintf("%s: anthropic: encode: %v", p.name, err))
	}
	req, err := http.NewRequestWithContext(tctx, http.MethodPost, p.anthropicURL(), bytes.NewReader(raw))
	if err != nil {
		return RequestError(fmt.Sprintf("%s: anthropic: %v", p.name, err))
	}
	p.anthropicHeaders(req, sessionID, true)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if parent.Err() != nil {
			return CancelledError()
		}
		return RequestError(fmt.Sprintf("%s: anthropic: %v", p.name, err))
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return p.statusError("anthropic", resp.StatusCode, string(data))
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
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var ev struct {
			Type         string `json:"type"`
			Index        int    `json:"index"`
			ContentBlock *struct {
				Type string `json:"type"`
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"content_block"`
			Delta *struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
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
		case "content_block_start":
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
			}
		case "message_stop":
			if calls := anthropicCalls(accs); len(calls) > 0 {
				if err := send(Chunk{ToolCalls: calls}); err != nil {
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
