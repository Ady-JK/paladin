// Package inference owns LLM providers: the Provider interface and
// the OpenAI-compatible implementation (backed by openai-go).
package inference

import (
	"context"

	"github.com/Ady-JK/paladin/core/internal/config"
)

type LlmMessage struct {
	Role       string
	Content    string
	ToolCalls  []ToolCall
	ToolCallID *string
	Name       *string
}

// ToolCall is one model-requested function invocation.
type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

type ToolDefinition struct {
	Name        string
	Description string
	Parameters  any
}

type ToolResponse struct {
	Content   string
	ToolCalls []ToolCall
}

// Chunk is one streamed piece: text, a tool-call notice (name arrived,
// args still streaming), final tool calls, or an error.
type Chunk struct {
	Text       string
	ToolCalls  []ToolCall
	ToolNotice *ToolCall
	Err        string
}

func TextChunk(text string) Chunk { return Chunk{Text: text} }

type Provider interface {
	Name() string
	ApiType() config.ApiType
	Model() string
	CompleteTools(ctx context.Context, system string, messages []LlmMessage, tools []ToolDefinition, sessionID string) (ToolResponse, error)
	StreamTools(ctx context.Context, system string, messages []LlmMessage, tools []ToolDefinition, sessionID string, tx chan<- Chunk, done <-chan struct{}) error
	ListModels(ctx context.Context) ([]string, error)
}

// ProviderError mirrors the Rust ProviderError Display strings.
type ProviderError struct {
	Kind    string
	Message string
}

func (e *ProviderError) Error() string {
	switch e.Kind {
	case "unknown":
		return "unknown provider: " + e.Message
	case "placeholder":
		return "provider has placeholder key: " + e.Message
	case "config":
		return "config: " + e.Message
	case "cancelled":
		return "cancelled"
	default:
		return e.Message
	}
}

func RequestError(msg string) *ProviderError { return &ProviderError{Kind: "request", Message: msg} }
func ConfigError(msg string) *ProviderError  { return &ProviderError{Kind: "config", Message: msg} }
func CancelledError() *ProviderError         { return &ProviderError{Kind: "cancelled", Message: "cancelled"} }
func IsCancelled(err error) bool {
	if pe, ok := err.(*ProviderError); ok {
		return pe.Kind == "cancelled"
	}
	return false
}

// BuildProvider mirrors the Rust build_provider: placeholder keys are
// rejected, anthropic/google dialects are unimplemented in v1.
func BuildProvider(apiType config.ApiType, name, model, baseURL, apiKey string) (Provider, error) {
	if config.IsPlaceholderKey(apiKey) {
		return nil, &ProviderError{Kind: "placeholder", Message: "provider " + quote(name) + ": api key is still the placeholder"}
	}
	switch apiType {
	case config.ApiTypeOpenai:
		return NewOpenAIProvider(name, model, baseURL, apiKey), nil
	case config.ApiTypeAnthropic:
		return nil, ConfigError("anthropic dialect not implemented in v1")
	case config.ApiTypeGoogle:
		return nil, ConfigError("google dialect not implemented in v1")
	default:
		return nil, ConfigError("unknown api type " + string(apiType))
	}
}

func quote(s string) string {
	// Rust {:?} on strings; %q matches for the ids we use.
	return "\"" + s + "\""
}
