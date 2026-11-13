// openai.go is the OpenAI-compatible provider.

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
	"sync/atomic"
	"time"

	"github.com/Ady-JK/paladin/core/internal/config"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/shared"
)

const (
	copilotAPIVersion    = "2026-08-01"
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
			return NewOpenAIProviderWithMeta(spec.Name, spec.Model, spec.BaseURL, spec.ApiKey, spec.Meta), nil
		},
	})
	RegisterProviderTemplate(ProviderTemplate{
		ApiType: config.ApiTypeCopilot,
		New: func(spec ProviderSpec) (Provider, error) {
			p := NewOpenAIProviderWithMeta(spec.Name, spec.Model, spec.BaseURL, spec.ApiKey, spec.Meta)
			client := openai.NewClient(
				option.WithBaseURL(p.baseURL), option.WithAPIKey(spec.ApiKey), option.WithHTTPClient(providerHTTPClient),
				option.WithMaxRetries(2), option.WithMiddleware(skipEmptySSEFrames()),
				option.WithHeader("Copilot-Integration-Id", "vscode-chat"),
				option.WithHeader("Editor-Version", "vscode/1.95.0"),
				option.WithHeader("Editor-Plugin-Version", "copilot-chat/0.26.7"),
				option.WithHeader("User-Agent", "GitHubCopilotChat/0.26.7"),
				option.WithHeader("Openai-Intent", "conversation-panel"),
			)
			p.client = &client
			p.isCopilot = true
			p.protocol = copilotProtocol(spec.Model, spec.Meta)
			return p, nil
		},
	})
}

type OpenAIProvider struct {
	name       string
	model      string
	baseURL    string
	apiKey     string
	isOpencode bool
	isCopilot  bool
	protocol   string
	// meta is what the provider's model listing said about this model;
	// the zero value means it said nothing.
	meta   config.ModelMeta
	client *openai.Client
	// effort is the reasoning effort sent with every call; empty sends
	// none and leaves the provider's default.
	effort string
	// effortDropped is set once the provider rejected effort.
	effortDropped *atomic.Bool
	// publicListing keeps the key off catalog reads: the listing is
	// public, and a placeholder key sent as a credential is refused.
	publicListing bool
}

// WithPublicListing marks p's catalog reads as unauthenticated. It is for
// a provider resolved without a real key, whose listing endpoint is
// public; a configured key is still sent, because it can unlock a
// private or regional listing.
//
// The flag switches the read to plain HTTP: the SDK always attaches the
// key it was built with (an empty key still sends "Bearer"), and its
// client also picks up OPENAI_API_KEY from the environment, which must
// never travel to a third-party gateway on a keyless read.
func WithPublicListing(p Provider) Provider {
	op, ok := p.(*OpenAIProvider)
	if !ok {
		return p
	}
	cp := *op
	cp.publicListing = true
	return &cp
}

func NewOpenAIProvider(name, model, baseURL, apiKey string) *OpenAIProvider {
	return NewOpenAIProviderWithMeta(name, model, baseURL, apiKey, config.ModelMeta{})
}

// NewOpenAIProviderWithMeta builds the provider knowing what the model
// listing said about model. The metadata decides the wire dialect (a
// gateway serving an OpenAI-named model over Responses, for instance)
// and the thinking levels it offers.
func NewOpenAIProviderWithMeta(name, model, baseURL, apiKey string, meta config.ModelMeta) *OpenAIProvider {
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
		option.WithHTTPClient(providerHTTPClient),
		option.WithMaxRetries(2),
		// Gateways emit keepalive frames with no data; the SDK's SSE
		// decoder chokes on them and kills a healthy stream (sse.go).
		option.WithMiddleware(skipEmptySSEFrames()),
	)
	isOpencode := config.IsOpencodeEndpoint(baseURL)
	return &OpenAIProvider{
		name:       name,
		model:      model,
		baseURL:    baseURL,
		apiKey:     apiKey,
		isOpencode: isOpencode,
		protocol:   ProtocolFor(baseURL, model, meta),
		meta:       meta,
		client:     &client,
	}
}

func (p *OpenAIProvider) Name() string            { return p.name }
func (p *OpenAIProvider) ApiType() config.ApiType { return config.ApiTypeOpenai }
func (p *OpenAIProvider) Model() string           { return p.model }

func (p *OpenAIProvider) headers(sessionID string) []option.RequestOption {
	if p.isCopilot && sessionID != "catalog" {
		return []option.RequestOption{option.WithHeader("X-GitHub-Api-Version", copilotAPIVersion)}
	}
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

func (p *OpenAIProvider) copilotOptions(messages []LlmMessage) []option.RequestOption {
	if !p.isCopilot {
		return nil
	}
	initiator := "user"
	vision := false
	for _, message := range messages {
		if message.Role == "assistant" || message.Role == "tool" {
			initiator = "agent"
		}
		vision = vision || message.Image != nil
	}
	opts := []option.RequestOption{option.WithHeader("X-Initiator", initiator)}
	if vision {
		opts = append(opts, option.WithHeader("Copilot-Vision-Request", "true"))
	}
	return opts
}

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
	var images []LlmMessage
	flushImages := func() {
		for _, m := range images {
			url, err := m.Image.DataURL()
			if err != nil {
				out = append(out, openai.UserMessage("An image from tool "+m.NameValue()+" is unavailable: "+err.Error()))
				continue
			}
			out = append(out, openai.UserMessage([]openai.ChatCompletionContentPartUnionParam{
				openai.TextContentPart("Image returned by tool " + m.NameValue() + ":"),
				openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{URL: url}),
			}))
		}
		images = nil
	}
	for _, m := range messages {
		if m.Role != "tool" {
			flushImages()
		}
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
			if m.Image != nil {
				images = append(images, m)
			}
		default:
			if m.Image == nil {
				out = append(out, openai.UserMessage(m.Content))
			} else if url, err := m.Image.DataURL(); err == nil {
				out = append(out, openai.UserMessage([]openai.ChatCompletionContentPartUnionParam{
					openai.TextContentPart(m.Content), openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{URL: url}),
				}))
			} else {
				out = append(out, openai.UserMessage(m.Content+" [image unavailable: "+err.Error()+"]"))
			}
		}
	}
	flushImages()
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

func (p *OpenAIProvider) newParams(system string, messages []LlmMessage, tools []ToolDefinition, sessionID string, streaming bool) openai.ChatCompletionNewParams {
	params := openai.ChatCompletionNewParams{
		Model:    p.model,
		Messages: toSDKMessages(messages),
	}
	if streaming {
		// The usage-bearing final chunk only arrives when asked for; it
		// is a standard field, harmless on plain OpenAI endpoints too.
		// It must only ride on streaming calls: upstreams reject a
		// non-streaming request carrying stream_options (this broke
		// /compact, which summarizes over the non-streaming call).
		params.StreamOptions = openai.ChatCompletionStreamOptionsParam{
			IncludeUsage: openai.Bool(true),
		}
	}
	if system != "" {
		sys := openai.SystemMessage(system)
		params.Messages = append([]openai.ChatCompletionMessageParamUnion{sys}, params.Messages...)
	}
	if len(tools) > 0 {
		params.Tools = toSDKTools(tools)
	}
	if p.effort != "" {
		params.ReasoningEffort = shared.ReasoningEffort(p.effort)
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
	var httpRes *http.Response
	opts := append(p.headers(sessionID), option.WithResponseInto(&httpRes))
	opts = append(opts, p.copilotOptions(messages)...)
	res, err := p.client.Chat.Completions.New(ctx, p.newParams(system, messages, tools, sessionID, false), opts...)
	if err != nil {
		return ToolResponse{}, p.mapError("chat completion", err)
	}
	if len(res.Choices) == 0 {
		return ToolResponse{}, RequestError(fmt.Sprintf("%s: chat completion returned no choices", p.name))
	}
	msg := res.Choices[0].Message
	out := ToolResponse{Content: msg.Content, Reasoning: deltaReasoning(msg.RawJSON())}
	for _, call := range msg.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, ToolCall{
			ID:        call.ID,
			Name:      call.Function.Name,
			Arguments: call.Function.Arguments,
		})
	}
	out.Usage = withHeaderCost(chatUsage(res.Usage, res.RawJSON()), httpRes)
	p.estimateCopilotUsage(out.Usage, false)
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
	captureCacheDetails(out, raw, "prompt_tokens_details")
	if out.TotalTokens == 0 {
		out.TotalTokens = out.PromptTokens + out.CompletionTokens
	}
	if ge, ok := gatewayCost(raw); ok {
		applyGatewayCost(out, ge.Cost, ge.ListCost, ge.HasCost, ge.HasList)
		if out.CacheReadTokens == 0 {
			out.CacheReadTokens = ge.CacheRead
		}
		if out.CacheWriteTokens == 0 {
			out.CacheWriteTokens = ge.CacheWrite
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
	opts := append(p.streamOptions(sessionID), p.copilotOptions(messages)...)
	stream := p.client.Chat.Completions.NewStreaming(tctx, p.newParams(system, messages, tools, sessionID, true), opts...)
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
			if r := deltaReasoning(choice.Delta.RawJSON()); r != "" {
				if err := send(Chunk{Reasoning: r}); err != nil {
					return CancelledError()
				}
			}
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
		case tx <- Chunk{Usage: p.estimateCopilotUsage(&u, false)}:
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
	// A body that stops mid-frame is a dropped connection, whatever the
	// decoder called it.
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, want := range []string{
		"eof", "connection reset", "connection refused", "broken pipe",
		"unexpected eof", "status 502", "status 503", "status 504",
		"bad gateway", "service unavailable", "gateway timeout",
		// Truncated or malformed frames from a gateway that dropped the
		// stream: the openai-go SSE decoder says "unexpected end of JSON
		// input" when the event data stopped short or arrived empty.
		"unexpected end of json", "unexpected end of file",
		"stream terminated", "stream closed", "connection closed",
		"reset by peer", "protocol error",
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
		if retryAfter == 0 {
			retryAfter = parseRetryHint(body)
		}
		return &ProviderError{Kind: "request", Message: msg, Status: apiErr.StatusCode, RetryAfter: retryAfter}
	}
	return RequestError(fmt.Sprintf("%s: %s: %v", p.name, op, err))
}

func (p *OpenAIProvider) ListModels(ctx context.Context) ([]string, error) {
	ids, _, err := p.ListCatalog(ctx)
	return ids, err
}

// ListCatalog lists the provider's models with whatever metadata its
// listing carries. Plain OpenAI-compatible endpoints publish ids only,
// so their metadata is nil and callers keep the built-in tables.
func (p *OpenAIProvider) ListCatalog(ctx context.Context) ([]string, map[string]CatalogMeta, error) {
	if p.isOpencode {
		return p.listOpencodeCatalog(ctx)
	}
	if p.publicListing {
		return p.listPublicCatalog(ctx)
	}
	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	opts := p.headers("catalog")
	if p.isCopilot {
		opts = append(opts, option.WithHeader("X-GitHub-Api-Version", copilotAPIVersion))
	}
	page, err := p.client.Models.List(ctx, opts...)
	var apiErr *openai.Error
	if p.isCopilot && errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusBadRequest {
		page, err = p.client.Models.List(ctx, p.headers("catalog")...)
	}
	if err != nil {
		return nil, nil, p.mapError("list models", err)
	}
	var ids []string
	var metadata map[string]CatalogMeta
	for _, m := range page.Data {
		if m.ID == "" {
			continue
		}
		if p.isCopilot {
			meta, include, err := parseCopilotModel(m.ID, m.RawJSON())
			if err != nil {
				return nil, nil, RequestError(fmt.Sprintf("%s: invalid Copilot model metadata", p.name))
			}
			if !include {
				continue
			}
			if metadata == nil {
				metadata = make(map[string]CatalogMeta)
			}
			metadata[m.ID] = meta
		}
		ids = append(ids, m.ID)
	}
	return ids, metadata, nil
}

// listPublicCatalog reads a keyless provider's listing over plain HTTP so
// no credential travels: not the provider's own, and not one the
// OpenAI SDK would pick up from the environment.
func (p *OpenAIProvider) listPublicCatalog(ctx context.Context) ([]string, map[string]CatalogMeta, error) {
	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	url := strings.TrimRight(p.baseURL, "/") + "/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, nil, RequestError(fmt.Sprintf("%s: list models: %v", p.name, err))
	}
	req.Header.Set("Accept", "application/json")
	resp, err := providerHTTPClient.Do(req)
	if err != nil {
		return nil, nil, RequestError(fmt.Sprintf("%s: list models: %v", p.name, err))
	}
	defer resp.Body.Close()
	body, err := readBody(resp.Body, maxResponseBody)
	if err != nil {
		return nil, nil, RequestError(fmt.Sprintf("%s: list models: %v", p.name, err))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil, p.requestError(resp.StatusCode, string(body))
	}
	ids, meta, err := ParseCatalog(body)
	if err != nil {
		return nil, nil, RequestError(fmt.Sprintf("%s: list models: %v", p.name, err))
	}
	return ids, meta, nil
}

func (p *OpenAIProvider) listOpencodeCatalog(ctx context.Context) ([]string, map[string]CatalogMeta, error) {
	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.catalogURL(), nil)
	if err != nil {
		return nil, nil, RequestError(fmt.Sprintf("%s: list models: %v", p.name, err))
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", config.OpencodeUserAgent)
	req.Header.Set(config.OpencodeSessionHeader, "catalog")
	// The catalog is public today, but sending the key when one is
	// configured keeps private/regional listings working without a
	// separate code path. A placeholder key must never be sent, and a
	// provider resolved without a real key marks itself publicListing:
	// the gateway refuses a bogus Bearer token where a header-less
	// request succeeds.
	if key := strings.TrimSpace(p.apiKey); key != "" && !config.IsPlaceholderKey(key) && !p.publicListing {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := providerHTTPClient.Do(req)
	if err != nil {
		return nil, nil, RequestError(fmt.Sprintf("%s: list models: %v", p.name, err))
	}
	defer resp.Body.Close()
	body, err := readBody(resp.Body, maxResponseBody)
	if err != nil {
		return nil, nil, RequestError(fmt.Sprintf("%s: list models: %v", p.name, err))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil, p.requestError(resp.StatusCode, string(body))
	}
	ids, meta, err := ParseCatalog(body)
	if err != nil {
		return nil, nil, RequestError(fmt.Sprintf("%s: list models: %v", p.name, err))
	}
	return ids, meta, nil
}
