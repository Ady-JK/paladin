// Package inference: openai.go is the OpenAI-compatible provider.
package inference

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/shared"

	"github.com/Ady-JK/paladin/core/internal/config"
)

const (
	DefaultOpenAIModel   = "gpt-4o-mini"
	DefaultOpenAIBaseURL = "https://api.openai.com/v1"
)

const (
	defaultTimeout = 60 * time.Second
	streamTimeout  = 90 * time.Second
	listTimeout    = 30 * time.Second
)

type OpenAIProvider struct {
	name       string
	model      string
	baseURL    string
	apiKey     string
	isOpencode bool
	client     *openai.Client
}

func NewOpenAIProvider(name, model, baseURL, apiKey string) *OpenAIProvider {
	if strings.TrimSpace(model) == "" {
		model = DefaultOpenAIModel
	}
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = DefaultOpenAIBaseURL
	}
	client := openai.NewClient(
		option.WithBaseURL(baseURL),
		option.WithAPIKey(apiKey),
		option.WithMaxRetries(0),
	)
	return &OpenAIProvider{
		name:       name,
		model:      model,
		baseURL:    baseURL,
		apiKey:     apiKey,
		isOpencode: config.IsOpencodeEndpoint(baseURL),
		client:     &client,
	}
}

func (p *OpenAIProvider) Name() string            { return p.name }
func (p *OpenAIProvider) ApiType() config.ApiType { return config.ApiTypeOpenai }
func (p *OpenAIProvider) Model() string           { return p.model }

func (p *OpenAIProvider) headers(sessionID string) []option.RequestOption {
	if !p.isOpencode {
		return nil
	}
	return []option.RequestOption{
		option.WithHeader("User-Agent", config.OpencodeUserAgent),
		option.WithHeader(config.OpencodeSessionHeader, sessionID),
	}
}

func (p *OpenAIProvider) chatURL() string { return p.baseURL + "/chat/completions" }

// requestError never logs or returns the key: errors carry status + body.
func (p *OpenAIProvider) requestError(status int, body string) *ProviderError {
	runes := []rune(strings.TrimSpace(body))
	short := string(runes)
	if len(runes) > 300 {
		short = string(runes[:300])
	}
	return RequestError(fmt.Sprintf("%s: chat completion: status %d: %s", p.name, status, short))
}

func toSDKMessages(messages []LlmMessage) []openai.ChatCompletionMessageParamUnion {
	out := make([]openai.ChatCompletionMessageParamUnion, 0, len(messages))
	for _, m := range messages {
		switch m.Role {
		case "assistant":
			u := openai.AssistantMessage(m.Content)
			if len(m.ToolCalls) > 0 && u.OfAssistant != nil {
				calls := make([]openai.ChatCompletionMessageToolCallParam, 0, len(m.ToolCalls))
				for _, c := range m.ToolCalls {
					calls = append(calls, openai.ChatCompletionMessageToolCallParam{
						ID: c.ID,
						Function: openai.ChatCompletionMessageToolCallFunctionParam{
							Name:      c.Name,
							Arguments: c.Arguments,
						},
					})
				}
				u.OfAssistant.ToolCalls = calls
			}
			out = append(out, u)
		case "tool":
			id := ""
			if m.ToolCallID != nil {
				id = *m.ToolCallID
			}
			out = append(out, openai.ToolMessage(m.Content, id))
		default:
			out = append(out, openai.UserMessage(m.Content))
		}
	}
	return out
}

func toSDKTools(tools []ToolDefinition) []openai.ChatCompletionToolParam {
	var out []openai.ChatCompletionToolParam
	for _, t := range tools {
		var params shared.FunctionParameters
		if t.Parameters != nil {
			if m, ok := t.Parameters.(map[string]any); ok {
				params = shared.FunctionParameters(m)
			} else if raw, err := json.Marshal(t.Parameters); err == nil {
				var m2 map[string]any
				if json.Unmarshal(raw, &m2) == nil {
					params = shared.FunctionParameters(m2)
				}
			}
		}
		out = append(out, openai.ChatCompletionToolParam{
			Function: shared.FunctionDefinitionParam{
				Name:        t.Name,
				Description: openai.String(t.Description),
				Parameters:  params,
			},
		})
	}
	return out
}

func (p *OpenAIProvider) newParams(system string, messages []LlmMessage, tools []ToolDefinition) openai.ChatCompletionNewParams {
	params := openai.ChatCompletionNewParams{
		Model:    shared.ChatModel(p.model),
		Messages: toSDKMessages(messages),
	}
	if system != "" {
		sys := openai.SystemMessage(system)
		params.Messages = append([]openai.ChatCompletionMessageParamUnion{sys}, params.Messages...)
	}
	if len(tools) > 0 {
		params.Tools = toSDKTools(tools)
	}
	return params
}

func (p *OpenAIProvider) CompleteTools(ctx context.Context, system string, messages []LlmMessage, tools []ToolDefinition, sessionID string) (ToolResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()
	res, err := p.client.Chat.Completions.New(ctx, p.newParams(system, messages, tools), p.headers(sessionID)...)
	if err != nil {
		return ToolResponse{}, p.mapError("chat completion", err)
	}
	if len(res.Choices) == 0 {
		return ToolResponse{}, RequestError(fmt.Sprintf("%s: chat completion returned no choices", p.name))
	}
	msg := res.Choices[0].Message
	out := ToolResponse{Content: msg.Content}
	for _, call := range msg.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, ToolCall{
			ID:        call.ID,
			Name:      call.Function.Name,
			Arguments: call.Function.Arguments,
		})
	}
	return out, nil
}

type callAcc struct {
	id        string
	name      string
	arguments strings.Builder
}

func (p *OpenAIProvider) StreamTools(ctx context.Context, system string, messages []LlmMessage, tools []ToolDefinition, sessionID string, tx chan<- Chunk, done <-chan struct{}) error {
	parent := ctx
	tctx, cancel := context.WithTimeout(ctx, streamTimeout)
	defer cancel()
	stream := p.client.Chat.Completions.NewStreaming(tctx, p.newParams(system, messages, tools), p.headers(sessionID)...)
	defer stream.Close()
	accs := map[int64]*callAcc{}
	signaled := map[int64]bool{}
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
	for stream.Next() {
		chunk := stream.Current()
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				if err := send(TextChunk(choice.Delta.Content)); err != nil {
					return CancelledError()
				}
			}
			for _, call := range choice.Delta.ToolCalls {
				acc := accs[call.Index]
				if acc == nil {
					acc = &callAcc{}
					accs[call.Index] = acc
				}
				if call.ID != "" && acc.id == "" {
					acc.id = call.ID
				}
				if call.Function.Name != "" && acc.name == "" {
					acc.name = call.Function.Name
				}
				acc.arguments.WriteString(call.Function.Arguments)
				if !signaled[call.Index] {
					signaled[call.Index] = true
					id := acc.id
					if id == "" {
						id = fmt.Sprintf("pending-%d", call.Index)
					}
					name := acc.name
					if name == "" {
						name = "tool"
					}
					if err := send(Chunk{ToolNotice: &ToolCall{ID: id, Name: name, Arguments: "{}"}}); err != nil {
						return CancelledError()
					}
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
	if err := stream.Err(); err != nil {
		if parent.Err() != nil {
			return CancelledError()
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return RequestError(fmt.Sprintf("%s: stream: the command ran past its timeout", p.name))
		}
		return p.mapError("stream", err)
	}
	if len(accs) > 0 {
		indexes := make([]int64, 0, len(accs))
		for i := range accs {
			indexes = append(indexes, i)
		}
		sort.Slice(indexes, func(a, b int) bool { return indexes[a] < indexes[b] })
		var calls []ToolCall
		for _, i := range indexes {
			acc := accs[i]
			id := acc.id
			if id == "" {
				id = fmt.Sprintf("pending-%d", i)
			}
			name := acc.name
			if name == "" {
				name = "tool"
			}
			calls = append(calls, ToolCall{ID: id, Name: name, Arguments: acc.arguments.String()})
		}
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

var errClientGone = CancelledError()

func isCancel(err error) bool {
	return errors.Is(err, context.Canceled)
}

func (p *OpenAIProvider) mapError(op string, err error) *ProviderError {
	if isCancel(err) {
		return CancelledError()
	}
	var apiErr *openai.Error
	if errors.As(err, &apiErr) && apiErr.StatusCode != 0 {
		body := strings.TrimSpace(apiErr.RawJSON())
		short := string([]rune(body))
		if len([]rune(body)) > 300 {
			short = string([]rune(body)[:300])
		}
		if short != "" {
			return RequestError(fmt.Sprintf("%s: %s: status %d: %s", p.name, op, apiErr.StatusCode, short))
		}
		return RequestError(fmt.Sprintf("%s: %s: status %d", p.name, op, apiErr.StatusCode))
	}
	return RequestError(fmt.Sprintf("%s: %s: %v", p.name, op, err))
}

func (p *OpenAIProvider) ListModels(ctx context.Context) ([]string, error) {
	if p.isOpencode {
		return p.listOpencodeCatalog(ctx)
	}
	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	page, err := p.client.Models.List(ctx, p.headers("catalog")...)
	if err != nil {
		return nil, p.mapError("list models", err)
	}
	var ids []string
	for _, m := range page.Data {
		if m.ID != "" {
			ids = append(ids, m.ID)
		}
	}
	return ids, nil
}

func (p *OpenAIProvider) listOpencodeCatalog(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", config.OpencodeCatalogURL, nil)
	if err != nil {
		return nil, RequestError(fmt.Sprintf("%s: list models: %v", p.name, err))
	}
	req.Header.Set("User-Agent", config.OpencodeUserAgent)
	req.Header.Set(config.OpencodeSessionHeader, "catalog")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, RequestError(fmt.Sprintf("%s: list models: %v", p.name, err))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, RequestError(fmt.Sprintf("%s: list models: %v", p.name, err))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, p.requestError(resp.StatusCode, string(body))
	}
	var wire struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, RequestError(fmt.Sprintf("%s: list models: decode: %v", p.name, err))
	}
	var ids []string
	for _, m := range wire.Data {
		if m.ID != "" {
			ids = append(ids, m.ID)
		}
	}
	return ids, nil
}
