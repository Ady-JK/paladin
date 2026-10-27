// Package inference owns LLM providers: the Provider interface, the
// provider template registry, and the OpenAI-compatible implementation
// (backed by openai-go).
package inference

import (
	"context"
	"sort"
	"sync"

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
	// Signature carries provider-specific round-trip data. Gemini puts a
	// thoughtSignature on a function-call part and rejects the next
	// request without it; other dialects leave it empty.
	Signature string
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

// ProviderSpec is the resolved config a provider template receives.
type ProviderSpec struct {
	Name    string
	Model   string
	BaseURL string
	ApiKey  string
}

// ProviderFactory builds a live provider from a resolved spec.
type ProviderFactory func(ProviderSpec) (Provider, error)

// ProviderTemplate is one dialect: the constructor for it. Adding a
// provider family means registering a template, not editing BuildProvider.
type ProviderTemplate struct {
	ApiType config.ApiType
	New     ProviderFactory
}

var (
	providerMu        sync.RWMutex
	providerTemplates = map[config.ApiType]ProviderTemplate{}
)

// RegisterProviderTemplate adds or replaces a dialect template.
func RegisterProviderTemplate(t ProviderTemplate) {
	if t.ApiType == "" || t.New == nil {
		return
	}
	providerMu.Lock()
	defer providerMu.Unlock()
	providerTemplates[t.ApiType] = t
}

// ProviderTemplates returns the registered dialects in sorted order.
func ProviderTemplates() []config.ApiType {
	providerMu.RLock()
	defer providerMu.RUnlock()
	out := make([]config.ApiType, 0, len(providerTemplates))
	for apiType := range providerTemplates {
		out = append(out, apiType)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func providerTemplate(apiType config.ApiType) (ProviderTemplate, bool) {
	providerMu.RLock()
	defer providerMu.RUnlock()
	t, ok := providerTemplates[apiType]
	return t, ok
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

// BuildProvider resolves a provider through its registered template.
// Placeholder keys are rejected for every dialect; unknown dialects and
// unimplemented ones keep their original error strings.
func BuildProvider(apiType config.ApiType, name, model, baseURL, apiKey string) (Provider, error) {
	if config.IsPlaceholderKey(apiKey) {
		return nil, &ProviderError{Kind: "placeholder", Message: "provider " + quote(name) + ": api key is still the placeholder"}
	}
	tpl, ok := providerTemplate(apiType)
	if !ok {
		return nil, ConfigError("unknown api type " + string(apiType))
	}
	return tpl.New(ProviderSpec{Name: name, Model: model, BaseURL: baseURL, ApiKey: apiKey})
}

func quote(s string) string {
	// Rust {:?} on strings; %q matches for the ids we use.
	return "\"" + s + "\""
}

func init() {
	RegisterProviderTemplate(ProviderTemplate{
		ApiType: config.ApiTypeAnthropic,
		New: func(ProviderSpec) (Provider, error) {
			return nil, ConfigError("anthropic dialect not implemented in v1")
		},
	})
	RegisterProviderTemplate(ProviderTemplate{
		ApiType: config.ApiTypeGoogle,
		New: func(ProviderSpec) (Provider, error) {
			return nil, ConfigError("google dialect not implemented in v1")
		},
	})
}
