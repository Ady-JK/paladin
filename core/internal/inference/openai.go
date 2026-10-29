// Package inference: openai.go is the OpenAI-compatible provider.
package inference

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	defaultTimeout = 180 * time.Second
	// Reasoning models (notably muse-spark over the Responses API) can
	// take several minutes on a first turn: tool planning + long output.
	// A 90s cap turned those slow-but-healthy turns into
	// "provider unavailable" errors, so stream generously.
	listTimeout = 30 * time.Second
)

func init() {
	// The OpenAI-compatible dialect is this package's own template.
	RegisterProviderTemplate(ProviderTemplate{
		ApiType: config.ApiTypeOpenai,
		New: func(spec ProviderSpec) (Provider, error) {
			return NewOpenAIProvider(spec.Name, spec.Model, spec.BaseURL, spec.ApiKey), nil
		},
	})
}

type OpenAIProvider struct {
	name       string
	model      string
	baseURL    string
	apiKey     string
	isOpencode bool
	protocol   string
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
		option.WithMaxRetries(2),
	)
	isOpencode := config.IsOpencodeEndpoint(baseURL)
	protocol := protocolChat
	if isOpencode {
		protocol = opencodeProtocol(model)
	}
	return &OpenAIProvider{
		name:       name,
		model:      model,
		baseURL:    baseURL,
		apiKey:     apiKey,
		isOpencode: isOpencode,
		protocol:   protocol,
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
	out := []option.RequestOption{
		option.WithHeader("User-Agent", config.OpencodeUserAgent),
	}
	// OpenCode asks every client for a stable per-conversation id; it also
	// routes and caches by it. Never send an empty value, which some
	// gateways reject as a missing header.
	if id := strings.TrimSpace(sessionID); id != "" {
		out = append(out, option.WithHeader(config.OpencodeSessionHeader, id))
	}
	return out
}

// streamOptions disables the SDK's hidden retries for streams: the agent
// loop owns retrying so it can honor Retry-After, back off visibly and
// never replay a partially streamed round.
func (p *OpenAIProvider) streamOptions(sessionID string) []option.RequestOption {
	return append(p.headers(sessionID), option.WithMaxRetries(0))
}

func (p *OpenAIProvider) chatURL() string { return p.baseURL + "/chat/completions" }

// catalogURL derives the model list endpoint from the configured base.
// OpenCode's documented base is .../go/openai/v1 while its model list is
// .../go/v1/models, so strip the protocol segment. The zen alias
// (.../zen/go/v1) uses .../zen/go/v1/models directly. Deriving keeps a
// custom or future OpenCode host from being pinned to the prod URL.
func (p *OpenAIProvider) catalogURL() string {
	u, err := url.Parse(p.baseURL)
	if err != nil {
		return config.OpencodeCatalogURL
	}
	path := strings.TrimRight(u.Path, "/")
	switch {
	case strings.HasSuffix(path, "/openai/v1"):
		path = strings.TrimSuffix(path, "/openai/v1") + "/v1/models"
	case strings.HasSuffix(path, "/v1"):
		path += "/models"
	default:
		path += "/v1/models"
	}
	u.Path = path
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

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

func (p *OpenAIProvider) newParams(system string, messages []LlmMessage, tools []ToolDefinition, sessionID string) openai.ChatCompletionNewParams {
	params := openai.ChatCompletionNewParams{
		Model:    shared.ChatModel(p.model),
		Messages: toSDKMessages(messages),
		// The usage-bearing final chunk only arrives when asked for; it is
		// a standard field, harmless on plain OpenAI endpoints too.
		StreamOptions: openai.ChatCompletionStreamOptionsParam{
			IncludeUsage: openai.Bool(true),
		},
	}
	if system != "" {
		sys := openai.SystemMessage(system)
		params.Messages = append([]openai.ChatCompletionMessageParamUnion{sys}, params.Messages...)
	}
	if len(tools) > 0 {
		params.Tools = toSDKTools(tools)
	}
	// OpenCode forwards this to the upstream provider. Pairing the stable
	// conversation id with the x-opencode-session header keeps the
	// prompt-cache bucket identical across every round of a turn.
	if p.isOpencode {
		if id := strings.TrimSpace(sessionID); id != "" {
			params.PromptCacheKey = openai.String(id)
		}
	}
	return params
}

func (p *OpenAIProvider) completeChat(ctx context.Context, system string, messages []LlmMessage, tools []ToolDefinition, sessionID string) (ToolResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()
	res, err := p.client.Chat.Completions.New(ctx, p.newParams(system, messages, tools, sessionID), p.headers(sessionID)...)
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
	out.Usage = chatUsage(res.Usage, res.RawJSON())
	return out, nil
}

// chatUsage maps a chat-completions usage block onto Usage. Token counts
// come from the typed SDK fields; dollar costs and the cache breakdown
// come from the OpenCode gateway extras in the raw body (absent on plain
// providers, where costs stay unreported).
func chatUsage(u openai.CompletionUsage, raw string) *Usage {
	out := &Usage{
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		ReasoningTokens:  u.CompletionTokensDetails.ReasoningTokens,
		CacheReadTokens:  u.PromptTokensDetails.CachedTokens,
		TotalTokens:      u.TotalTokens,
	}
	if out.TotalTokens == 0 {
		out.TotalTokens = out.PromptTokens + out.CompletionTokens
	}
	if cost, list, cr, cw, ok := gatewayCost(raw); ok {
		out.Cost, out.ListCost, out.HasCost = cost, list, true
		if out.CacheReadTokens == 0 {
			out.CacheReadTokens = cr
		}
		if out.CacheWriteTokens == 0 {
			out.CacheWriteTokens = cw
		}
	}
	if *out == (Usage{}) {
		return nil
	}
	return out
}

type callAcc struct {
	id        string
	name      string
	arguments strings.Builder
}

func (p *OpenAIProvider) streamChat(ctx context.Context, system string, messages []LlmMessage, tools []ToolDefinition, sessionID string, tx chan<- Chunk, done <-chan struct{}) error {
	parent := ctx
	tctx, idle, cancel := watchIdle(ctx)
	defer cancel()
	stream := p.client.Chat.Completions.NewStreaming(tctx, p.newParams(system, messages, tools, sessionID), p.streamOptions(sessionID)...)
	defer stream.Close()
	var usage Usage
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
		idle.touch()
		chunk := stream.Current()
		if u := chatUsage(chunk.Usage, chunk.RawJSON()); u != nil {
			usage.Add(*u)
		}
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
		if idle.Fired() {
			return idleError(p.name, "stream")
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
	// Usage travels as its own trailing chunk so the loop can bill the
	// turn even when the model made no tool calls.
	if !usage.Empty() {
		u := usage
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

var errClientGone = CancelledError()

// isTransientTransport reports transport blips worth one immediate
// retry (gateway EOF / reset before any data arrived, 5xx): the model
// never saw the turn, so resending is safe.
func isTransientTransport(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, want := range []string{
		"eof", "connection reset", "connection refused", "broken pipe",
		"unexpected eof", "status 502", "status 503", "status 504",
		"bad gateway", "service unavailable", "gateway timeout",
	} {
		if strings.Contains(msg, want) {
			return true
		}
	}
	return false
}

func isCancel(err error) bool {
	return errors.Is(err, context.Canceled)
}

func (p *OpenAIProvider) mapError(op string, err error) *ProviderError {
	if isCancel(err) {
		return CancelledError()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &ProviderError{Kind: "request", Timeout: true,
			Message: fmt.Sprintf("%s: %s: model response timed out after %s — the model was still working; try again", p.name, op, defaultTimeout)}
	}
	if isTransientTransport(err) {
		return RequestError(fmt.Sprintf("%s: %s: transient connection (%v) — try again", p.name, op, err))
	}
	var apiErr *openai.Error
	if errors.As(err, &apiErr) && apiErr.StatusCode != 0 {
		body := strings.TrimSpace(apiErr.RawJSON())
		short := string([]rune(body))
		if len([]rune(body)) > 300 {
			short = string([]rune(body)[:300])
		}
		msg := fmt.Sprintf("%s: %s: status %d", p.name, op, apiErr.StatusCode)
		if short != "" {
			msg += ": " + short
		}
		var retryAfter time.Duration
		if apiErr.Response != nil {
			retryAfter = parseRetryAfter(apiErr.Response.Header, time.Now())
		}
		return &ProviderError{Kind: "request", Message: msg, Status: apiErr.StatusCode, RetryAfter: retryAfter}
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
	req, err := http.NewRequestWithContext(ctx, "GET", p.catalogURL(), nil)
	if err != nil {
		return nil, RequestError(fmt.Sprintf("%s: list models: %v", p.name, err))
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", config.OpencodeUserAgent)
	req.Header.Set(config.OpencodeSessionHeader, "catalog")
	// The catalog is public today, but sending the key when one is
	// configured keeps private/regional listings working without a
	// separate code path. A placeholder key must never be sent.
	if key := strings.TrimSpace(p.apiKey); key != "" && !config.IsPlaceholderKey(key) {
		req.Header.Set("Authorization", "Bearer "+key)
	}
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
