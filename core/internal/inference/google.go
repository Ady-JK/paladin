// google.go implements the Google Generative Language
// dialect used by OpenCode Go for Gemini models.

package inference

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type googlePart struct {
	Text             string            `json:"text,omitempty"`
	InlineData       *googleInlineData `json:"inlineData,omitempty"`
	ThoughtSignature string            `json:"thoughtSignature,omitempty"`
	FunctionCall     *googleFunction   `json:"functionCall,omitempty"`
	FunctionResp     *googleFunctionR  `json:"functionResponse,omitempty"`
}

type googleInlineData struct {
	MIMEType string `json:"mimeType"`
	Data     string `json:"data"`
}

type googleFunction struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name"`
	Args any    `json:"args,omitempty"`
}

type googleFunctionR struct {
	Name     string `json:"name"`
	Response any    `json:"response"`
}

type googleContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []googlePart `json:"parts"`
}

type googleFunctionDecl struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type googleRequest struct {
	SystemInstruction *googleContent  `json:"systemInstruction,omitempty"`
	Contents          []googleContent `json:"contents"`
	Tools             []struct {
		FunctionDeclarations []googleFunctionDecl `json:"functionDeclarations"`
	} `json:"tools,omitempty"`
}

func googleToolResponse(content string) any {
	var v any
	if json.Unmarshal([]byte(content), &v) == nil {
		if _, ok := v.(map[string]any); ok {
			return v
		}
	}
	return map[string]any{"output": content}
}

func googleToolName(m LlmMessage) string {
	if m.Name != nil && strings.TrimSpace(*m.Name) != "" {
		return *m.Name
	}
	return "tool"
}

func toGoogleRequest(system string, messages []LlmMessage, tools []ToolDefinition) googleRequest {
	req := googleRequest{Contents: []googleContent{}}
	if system != "" {
		req.SystemInstruction = &googleContent{Parts: []googlePart{{Text: system}}}
	}
	var pendingTools []googlePart
	flushTools := func() {
		if len(pendingTools) == 0 {
			return
		}
		req.Contents = append(req.Contents, googleContent{Role: "user", Parts: pendingTools})
		pendingTools = nil
	}
	for _, m := range messages {
		switch m.Role {
		case "assistant":
			var parts []googlePart
			if m.Content != "" {
				parts = append(parts, googlePart{Text: m.Content})
			}
			for _, c := range m.ToolCalls {
				parts = append(parts, googlePart{
					ThoughtSignature: c.Signature,
					FunctionCall:     &googleFunction{ID: c.ID, Name: c.Name, Args: toolInput(c.Arguments)},
				})
			}
			if len(parts) > 0 {
				flushTools()
				req.Contents = append(req.Contents, googleContent{Role: "model", Parts: parts})
			}
		case "tool":
			pendingTools = append(pendingTools, googlePart{FunctionResp: &googleFunctionR{
				Name:     googleToolName(m),
				Response: googleToolResponse(m.Content),
			}})
			if m.Image != nil {
				if url, err := m.Image.DataURL(); err == nil {
					_, data, _ := strings.Cut(url, ",")
					pendingTools = append(pendingTools, googlePart{InlineData: &googleInlineData{MIMEType: m.Image.MIMEType, Data: data}})
				} else {
					pendingTools = append(pendingTools, googlePart{Text: "Image unavailable: " + err.Error()})
				}
			}
		case "system":
			// Instructions live in systemInstruction.
		default:
			if len(pendingTools) > 0 {
				pendingTools = append(pendingTools, googlePart{Text: m.Content})
				flushTools()
				continue
			}
			req.Contents = append(req.Contents, googleContent{
				Role: "user", Parts: []googlePart{{Text: m.Content}},
			})
		}
	}
	flushTools()
	var decls []googleFunctionDecl
	for _, t := range tools {
		decls = append(decls, googleFunctionDecl{
			Name: t.Name, Description: t.Description, Parameters: schemaMap(t.Parameters),
		})
	}
	if len(decls) > 0 {
		req.Tools = []struct {
			FunctionDeclarations []googleFunctionDecl `json:"functionDeclarations"`
		}{{FunctionDeclarations: decls}}
	}
	return req
}

func (p *OpenAIProvider) completeGoogle(ctx context.Context, system string, messages []LlmMessage, tools []ToolDefinition, sessionID string) (ToolResponse, error) {
	body := toGoogleRequest(system, messages, tools)
	raw, err := json.Marshal(body)
	if err != nil {
		return ToolResponse{}, RequestError(fmt.Sprintf("%s: google: encode: %v", p.name, err))
	}
	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.googleURL(false), bytes.NewReader(raw))
	if err != nil {
		return ToolResponse{}, RequestError(fmt.Sprintf("%s: google: %v", p.name, err))
	}
	p.googleHeaders(req, sessionID, false)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return ToolResponse{}, CancelledError()
		}
		return ToolResponse{}, RequestError(fmt.Sprintf("%s: google: %v", p.name, err))
	}
	defer resp.Body.Close()
	data, err := readBody(resp.Body, maxResponseBody)
	if err != nil {
		return ToolResponse{}, RequestError(fmt.Sprintf("%s: google: %v", p.name, err))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ToolResponse{}, statusErrorFrom(p.name, "google", resp, string(data))
	}
	var wire googleResponse
	if err := json.Unmarshal(data, &wire); err != nil {
		return ToolResponse{}, RequestError(fmt.Sprintf("%s: google: decode: %v", p.name, err))
	}
	if wire.Error != nil && wire.Error.Message != "" {
		return ToolResponse{}, RequestError(fmt.Sprintf("%s: google: %s", p.name, wire.Error.Message))
	}
	out := wire.toolResponse()
	out.Usage = googleUsageOf(wire.UsageMetadata)
	return out, nil
}

func (p *OpenAIProvider) googleHeaders(req *http.Request, sessionID string, stream bool) {
	req.Header.Set("Content-Type", "application/json")
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	} else {
		req.Header.Set("Accept", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	p.setSessionHeaders(req, sessionID)
}

type googleResponse struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text             string `json:"text"`
				ThoughtSignature string `json:"thoughtSignature"`
				FunctionCall     *struct {
					ID   string          `json:"id"`
					Name string          `json:"name"`
					Args json.RawMessage `json:"args"`
				} `json:"functionCall"`
			} `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
	UsageMetadata *googleUsage `json:"usageMetadata"`
	Error         *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// googleUsage is the Generative Language usage envelope. Field names
// follow the API; the OpenCode gateway additionally nests its cost
// envelope here (cost/opencode), captured the same way as chat usage.
type googleUsage struct {
	PromptTokens        int64    `json:"promptTokenCount"`
	CandidatesTokens    int64    `json:"candidatesTokenCount"`
	ThoughtsTokens      int64    `json:"thoughtsTokenCount"`
	CachedContentTokens int64    `json:"cachedContentTokenCount"`
	TotalTokens         int64    `json:"totalTokenCount"`
	Cost                *float64 `json:"cost"`
	Opencode            *struct {
		ListCost *float64 `json:"list_cost"`
		Tokens   *struct {
			CacheWrite5m int64 `json:"cache_write_5m"`
			CacheWrite1h int64 `json:"cache_write_1h"`
		} `json:"tokens"`
	} `json:"opencode"`
}

func googleUsageOf(m *googleUsage) *Usage {
	if m == nil {
		return nil
	}
	out := &Usage{
		PromptTokens:     m.PromptTokens,
		CompletionTokens: m.CandidatesTokens,
		ReasoningTokens:  m.ThoughtsTokens,
		CacheReadTokens:  m.CachedContentTokens,
		TotalTokens:      m.TotalTokens,
	}
	if out.TotalTokens == 0 {
		out.TotalTokens = out.PromptTokens + out.CompletionTokens
	}
	if m.Cost != nil {
		out.Cost, out.HasCost = *m.Cost, true
	}
	if m.Opencode != nil {
		if m.Opencode.ListCost != nil {
			out.ListCost = *m.Opencode.ListCost
			out.HasCost = true
		}
		if m.Opencode.Tokens != nil {
			out.CacheWriteTokens = m.Opencode.Tokens.CacheWrite5m + m.Opencode.Tokens.CacheWrite1h
		}
	}
	if *out == (Usage{}) {
		return nil
	}
	return out
}

func (r googleResponse) toolResponse() ToolResponse {
	var out ToolResponse
	var text strings.Builder
	call := 0
	for _, cand := range r.Candidates {
		for _, part := range cand.Content.Parts {
			if part.Text != "" {
				text.WriteString(part.Text)
			}
			if part.FunctionCall != nil {
				args := string(part.FunctionCall.Args)
				if strings.TrimSpace(args) == "" {
					args = "{}"
				}
				id := part.FunctionCall.ID
				if id == "" {
					id = fmt.Sprintf("gemini-%d-%d", time.Now().UnixNano(), call)
				}
				out.ToolCalls = append(out.ToolCalls, ToolCall{
					ID:        id,
					Name:      part.FunctionCall.Name,
					Arguments: args,
					Signature: part.ThoughtSignature,
				})
				call++
			}
		}
	}
	out.Content = text.String()
	return out
}

func (p *OpenAIProvider) streamGoogle(ctx context.Context, system string, messages []LlmMessage, tools []ToolDefinition, sessionID string, tx chan<- Chunk, done <-chan struct{}) error {
	parent := ctx
	tctx, idle, cancel := watchIdle(ctx)
	defer cancel()
	body := toGoogleRequest(system, messages, tools)
	raw, err := json.Marshal(body)
	if err != nil {
		return RequestError(fmt.Sprintf("%s: google: encode: %v", p.name, err))
	}
	req, err := http.NewRequestWithContext(tctx, http.MethodPost, p.googleURL(true), bytes.NewReader(raw))
	if err != nil {
		return RequestError(fmt.Sprintf("%s: google: %v", p.name, err))
	}
	p.googleHeaders(req, sessionID, true)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if parent.Err() != nil {
			return CancelledError()
		}
		if idle.Fired() {
			return idleError(p.name, "google")
		}
		return RequestError(fmt.Sprintf("%s: google: %v", p.name, err))
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return statusErrorFrom(p.name, "google", resp, readErrorBody(resp.Body))
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
	var calls []ToolCall
	var streamUsage Usage
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
		var ev googleResponse
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			continue
		}
		if ev.Error != nil && ev.Error.Message != "" {
			return RequestError(fmt.Sprintf("%s: google: %s", p.name, ev.Error.Message))
		}
		// usageMetadata repeats on chunks with running totals; the last
		// one is authoritative, so it overwrites rather than adds.
		if u := googleUsageOf(ev.UsageMetadata); u != nil {
			streamUsage = *u
		}
		for _, cand := range ev.Candidates {
			for _, part := range cand.Content.Parts {
				if part.Text != "" {
					if err := send(TextChunk(part.Text)); err != nil {
						return CancelledError()
					}
				}
				if part.FunctionCall != nil {
					args := string(part.FunctionCall.Args)
					if strings.TrimSpace(args) == "" {
						args = "{}"
					}
					id := part.FunctionCall.ID
					if id == "" {
						id = fmt.Sprintf("gemini-%d-%d", time.Now().UnixNano(), len(calls))
					}
					call := ToolCall{
						ID:        id,
						Name:      part.FunctionCall.Name,
						Arguments: args,
						Signature: part.ThoughtSignature,
					}
					if err := send(Chunk{ToolNotice: &ToolCall{ID: call.ID, Name: call.Name, Arguments: "{}"}}); err != nil {
						return CancelledError()
					}
					calls = append(calls, call)
				}
			}
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
		return idleError(p.name, "google")
	}
	if err := scanner.Err(); err != nil {
		if parent.Err() != nil {
			return CancelledError()
		}
		return RequestError(fmt.Sprintf("%s: google: %v", p.name, err))
	}
	if len(calls) > 0 {
		select {
		case tx <- Chunk{ToolCalls: calls}:
		case <-done:
			return CancelledError()
		case <-parent.Done():
			return CancelledError()
		}
	}
	if !streamUsage.Empty() {
		u := streamUsage
		select {
		case tx <- Chunk{Usage: &u}:
		case <-done:
			return CancelledError()
		case <-parent.Done():
			return CancelledError()
		}
	}
	return nil
}
