// The system prompt file and its settings-UI accessors.

package runtime

import (
	"fmt"
	"strings"
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
