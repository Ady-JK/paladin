// The system prompt file and its settings-UI accessors.

package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Ady-JK/paladin/core/internal/inference"
)

// Prompts is the user-editable agent prompt: the system prompt file
// (config/SYSTEM_PROMPT.md). The file is authoritative — it wins over
// every default; only an explicit per-request override beats it, and
// the settings UI edits it. Tool guidance and examples are compiled
// per-OS in code and always appended with the machine + workspace
// environment block.
type Prompts struct {
	SystemPrompt string `json:"systemPrompt"`
}

// runtimePrompts adapts the prompt file to agent.PromptSource.
type runtimePrompts struct{ r *AgentRuntime }

func (p runtimePrompts) SystemPrompt(override string) string { return p.r.systemPrompt(override) }

func (r *AgentRuntime) systemPrompt(override string) string {
	if strings.TrimSpace(override) != "" {
		return override
	}
	return r.store.LoadSystemPrompt()
}

// GetPrompt returns the current prompt file content for the settings
// UI.
func (r *AgentRuntime) GetPrompt() Prompts {
	return Prompts{SystemPrompt: r.store.LoadSystemPrompt()}
}

// SetPrompt replaces the prompt file. Blank text is rejected.
func (r *AgentRuntime) SetPrompt(systemPrompt string) (Prompts, error) {
	if strings.TrimSpace(systemPrompt) == "" {
		return Prompts{}, fmt.Errorf("parse: system prompt must not be empty")
	}
	if err := r.store.SaveSystemPrompt(systemPrompt); err != nil {
		return Prompts{}, err
	}
	return r.GetPrompt(), nil
}

// ResetPrompt rewrites the prompt file from the built-in default.
func (r *AgentRuntime) ResetPrompt() (Prompts, error) {
	if err := r.store.ResetPrompt(); err != nil {
		return Prompts{}, err
	}
	return r.GetPrompt(), nil
}

// SuggestPrompt returns a draft only. The caller must explicitly apply and
// save it; this method never changes the prompt file or conversation.
func (r *AgentRuntime) SuggestPrompt(prompt, instruction, providerName, model string) (Prompts, error) {
	if strings.TrimSpace(prompt) == "" || strings.TrimSpace(instruction) == "" {
		return Prompts{}, fmt.Errorf("parse: prompt and instruction are required")
	}
	if len(prompt) > 128*1024 || len(instruction) > 4096 {
		return Prompts{}, fmt.Errorf("parse: prompt or instruction is too long")
	}
	provider, err := runtimeProviders{r}.ResolveProvider(providerName, model)
	if err != nil {
		return Prompts{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	response, err := provider.CompleteTools(ctx,
		"You edit coding-agent system prompts. Return only the complete revised prompt as Markdown. Preserve existing tool contracts and safety rules unless the user explicitly asks to change them. Do not wrap the result in a code fence.",
		[]inference.LlmMessage{{Role: "user", Content: "Current prompt:\n<current_prompt>\n" + prompt + "\n</current_prompt>\n\nRequested change:\n" + instruction}}, nil, "")
	if err != nil {
		return Prompts{}, fmt.Errorf("prompt proposal: %w", err)
	}
	proposal := strings.TrimSpace(response.Content)
	if proposal == "" {
		return Prompts{}, fmt.Errorf("prompt proposal: model returned no text")
	}
	return Prompts{SystemPrompt: proposal}, nil
}
