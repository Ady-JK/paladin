// Package inference: opencode.go routes OpenCode Go models to the protocol
// their gateway exposes. OpenCode serves most models over OpenAI chat
// completions, but some (GPT, Grok, Muse) only over the Responses API.
package inference

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/Ady-JK/paladin/core/internal/config"
)

const (
	protocolChat      = "chat"
	protocolResponses = "responses"
	protocolAnthropic = "anthropic"
	protocolGoogle    = "google"
)

// setSessionHeaders stamps the OpenCode headers on a raw-HTTP request
// (the Anthropic/Google dialects; the OpenAI client uses option headers).
func (p *OpenAIProvider) setSessionHeaders(req *http.Request, sessionID string) {
	if !p.isOpencode {
		return
	}
	req.Header.Set("User-Agent", config.OpencodeUserAgent)
	if id := strings.TrimSpace(sessionID); id != "" {
		req.Header.Set(config.OpencodeSessionHeader, id)
	}
}

// statusError keeps the status and a short body, never the key.
func (p *OpenAIProvider) statusError(op string, status int, body string) *ProviderError {
	short := string([]rune(strings.TrimSpace(body)))
	if len([]rune(short)) > 300 {
		short = string([]rune(short)[:300])
	}
	if short == "" {
		return RequestError(fmt.Sprintf("%s: %s: status %d", p.name, op, status))
	}
	return RequestError(fmt.Sprintf("%s: %s: status %d: %s", p.name, op, status, short))
}

// gatewayRoot strips the OpenAI protocol suffix from an OpenCode Go base,
// leaving the shared gateway prefix. The default base
// https://opencode.ai/inference/go/openai/v1 becomes
// https://opencode.ai/inference/go; the zen alias .../zen/go/v1 becomes
// .../zen/go. Anthropic and Google endpoints hang off that prefix.
func gatewayRoot(base string) (*url.URL, bool) {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return nil, false
	}
	path := strings.TrimRight(u.Path, "/")
	switch {
	case strings.HasSuffix(path, "/openai/v1"):
		path = strings.TrimSuffix(path, "/openai/v1")
	case strings.HasSuffix(path, "/v1"):
		path = strings.TrimSuffix(path, "/v1")
	case strings.HasSuffix(path, "/openai"):
		path = strings.TrimSuffix(path, "/openai")
	}
	u.Path = path
	u.RawQuery = ""
	u.Fragment = ""
	return u, true
}

func (p *OpenAIProvider) gatewayURL(pathSuffix, rawQuery string) string {
	u, ok := gatewayRoot(p.baseURL)
	if !ok {
		return p.baseURL + pathSuffix
	}
	u.Path = strings.TrimRight(u.Path, "/") + pathSuffix
	u.RawQuery = rawQuery
	return u.String()
}

func (p *OpenAIProvider) anthropicURL() string {
	return p.gatewayURL("/anthropic/v1/messages", "")
}

func (p *OpenAIProvider) googleURL(stream bool) string {
	suffix := "/google/v1beta/models/" + url.PathEscape(p.model)
	if stream {
		return p.gatewayURL(suffix+":streamGenerateContent", "alt=sse")
	}
	return p.gatewayURL(suffix+":generateContent", "")
}

// opencodeProtocol maps a model id to the wire protocol OpenCode Go
// documents for it. Unknown models stay on chat completions, which is the
// compatibility path most of the catalog uses.
func opencodeProtocol(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.HasPrefix(m, "gpt-"),
		strings.HasPrefix(m, "grok-"),
		strings.HasPrefix(m, "muse-"),
		strings.HasPrefix(m, "omen-"):
		return protocolResponses
	case strings.HasPrefix(m, "claude-"):
		return protocolAnthropic
	case strings.HasPrefix(m, "gemini-"):
		return protocolGoogle
	default:
		return protocolChat
	}
}

// CompleteTools dispatches to the protocol OpenCode exposes for this
// model. Non-OpenCode providers always use chat completions.
func (p *OpenAIProvider) CompleteTools(ctx context.Context, system string, messages []LlmMessage, tools []ToolDefinition, sessionID string) (ToolResponse, error) {
	switch p.protocol {
	case protocolResponses:
		return p.completeResponses(ctx, system, messages, tools, sessionID)
	case protocolAnthropic:
		return p.completeAnthropic(ctx, system, messages, tools, sessionID)
	case protocolGoogle:
		return p.completeGoogle(ctx, system, messages, tools, sessionID)
	default:
		return p.completeChat(ctx, system, messages, tools, sessionID)
	}
}

// StreamTools is the streaming counterpart of CompleteTools.
func (p *OpenAIProvider) StreamTools(ctx context.Context, system string, messages []LlmMessage, tools []ToolDefinition, sessionID string, tx chan<- Chunk, done <-chan struct{}) error {
	switch p.protocol {
	case protocolResponses:
		return p.streamResponses(ctx, system, messages, tools, sessionID, tx, done)
	case protocolAnthropic:
		return p.streamAnthropic(ctx, system, messages, tools, sessionID, tx, done)
	case protocolGoogle:
		return p.streamGoogle(ctx, system, messages, tools, sessionID, tx, done)
	default:
		return p.streamChat(ctx, system, messages, tools, sessionID, tx, done)
	}
}
