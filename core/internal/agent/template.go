// Package agent: template.go is the composition layer. A Template is a
// reusable blueprint (prompt, tool set, round cap); Build resolves it
// against runtime dependencies into a ready-to-run Agent.
package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/Ady-JK/paladin/core/internal/config"
	"github.com/Ady-JK/paladin/core/internal/inference"
	"github.com/Ady-JK/paladin/core/internal/prompt"
	"github.com/Ady-JK/paladin/core/internal/tools"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

// Template is the reusable blueprint for one kind of agent. The zero
// value is valid: it uses the runtime's default prompt, all registered
// tools, and the runtime round cap.
type Template struct {
	// Name identifies the template in a Registry.
	Name string `json:"name"`
	// SystemPrompt overrides the runtime base prompt when no explicit
	// per-request override is supplied.
	SystemPrompt string `json:"system_prompt,omitempty"`
	// ToolGuidance overrides the runtime tool guidance block.
	ToolGuidance string `json:"tool_guidance,omitempty"`
	// Tools selects a subset by name. Empty means every tool in the
	// runtime registry.
	Tools []string `json:"tools,omitempty"`
	// MaxRounds caps tool rounds for this agent. Zero means the runtime
	// default.
	MaxRounds uint32 `json:"max_tool_rounds,omitempty"`
}

// DefaultTemplate is the built-in single agent.
func DefaultTemplate() Template {
	return Template{Name: "default"}
}

// SessionSource loads the transcript for a turn.
type SessionSource interface {
	LoadSession(cwd, sessionID string) (transcript.Session, error)
}

// ProviderSource resolves the provider that will serve the turn.
type ProviderSource interface {
	ResolveProvider(name, model string) (inference.Provider, error)
}

// PromptSource supplies the runtime defaults a template can override.
type PromptSource interface {
	SystemPrompt(override string) string
	ToolGuidance() string
}

// Deps is the runtime-owned set of collaborators Build needs. Keeping
// them behind interfaces lets a turn be built without a live runtime.
type Deps struct {
	Sessions  SessionSource
	Providers ProviderSource
	Prompts   PromptSource
	Tools     *tools.Registry
	Shell     *tools.ShellTool
	MaxRounds uint32
	Context   context.Context
	Cancel    context.CancelFunc
	Tx        chan<- ChatChunk
	Done      <-chan struct{}
}

// Build resolves a Template into an Agent. Everything the loop needs
// (session, history, provider, tool subset, prompt, limits) is fixed
// here, before any model call happens.
func (t Template) Build(d Deps, req Request) (*Agent, error) {
	if strings.TrimSpace(req.SessionID) == "" {
		return nil, fmt.Errorf("sessionId is required: create one with open_session first")
	}
	if strings.TrimSpace(req.Message) == "" && !req.Resume {
		return nil, fmt.Errorf("message is required")
	}
	if d.Sessions == nil || d.Providers == nil || d.Prompts == nil {
		return nil, fmt.Errorf("agent template %q: missing runtime dependencies", t.Name)
	}
	if d.Tools == nil {
		return nil, fmt.Errorf("agent template %q: tool registry is required", t.Name)
	}

	sess, err := d.Sessions.LoadSession(req.Cwd, req.SessionID)
	if err != nil {
		return nil, err
	}
	provider, err := d.Providers.ResolveProvider(strVal(req.Provider), strVal(req.Model))
	if err != nil {
		return nil, err
	}
	history, err := sess.History()
	if err != nil {
		return nil, err
	}

	base := d.Prompts.SystemPrompt(strVal(req.SystemPrompt))
	if strings.TrimSpace(strVal(req.SystemPrompt)) == "" && strings.TrimSpace(t.SystemPrompt) != "" {
		base = t.SystemPrompt
	}
	guidance := d.Prompts.ToolGuidance()
	if strings.TrimSpace(t.ToolGuidance) != "" {
		guidance = t.ToolGuidance
	}
	system := prompt.ToolSystem(base, guidance, sess.Meta.Cwd)

	registry := d.Tools
	shell := d.Shell
	if len(t.Tools) > 0 {
		subset, err := d.Tools.Subset(t.Tools)
		if err != nil {
			return nil, fmt.Errorf("agent template %q: %w", t.Name, err)
		}
		registry = subset
		shell = nil
		if raw, ok := subset.Get("shell"); ok {
			shell, _ = raw.(*tools.ShellTool)
		}
	}

	maxRounds := d.MaxRounds
	if t.MaxRounds > 0 {
		maxRounds = config.ClampMaxToolRounds(t.MaxRounds)
	}

	return newAgent(runConfig{
		session:   sess,
		provider:  provider,
		tools:     registry,
		shell:     shell,
		toolDefs:  toolDefinitions(registry),
		history:   history,
		system:    system,
		maxRounds: maxRounds,
		ctx:       d.Context,
		cancel:    d.Cancel,
		tx:        d.Tx,
		done:      d.Done,
		message:   req.Message,
		resume:    req.Resume,
	}), nil
}

func toolDefinitions(registry *tools.Registry) []inference.ToolDefinition {
	var out []inference.ToolDefinition
	for _, def := range registry.Definitions() {
		out = append(out, inference.ToolDefinition{
			Name:        def.Name,
			Description: def.Description,
			Parameters:  def.Parameters,
		})
	}
	return out
}

func strVal(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// Registry is a named set of agent templates. It is the lookup point
// for future sub-agents and graph nodes: one node asks the registry for
// the template it wants instead of hard-coding a constructor.
type Registry struct {
	mu     sync.RWMutex
	byName map[string]Template
	order  []string
}

func NewRegistry() *Registry {
	return &Registry{byName: map[string]Template{}}
}

func (r *Registry) Register(t Template) error {
	t.Name = strings.TrimSpace(t.Name)
	if t.Name == "" {
		return fmt.Errorf("agent template: name is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.byName[t.Name]; !exists {
		r.order = append(r.order, t.Name)
	}
	r.byName[t.Name] = t
	return nil
}

func (r *Registry) Get(name string) (Template, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.byName[name]
	return t, ok
}

func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, len(r.order))
	copy(out, r.order)
	return out
}

// Default returns "default" when registered, else the first template.
func (r *Registry) Default() (Template, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if t, ok := r.byName["default"]; ok {
		return t, true
	}
	for _, name := range r.order {
		return r.byName[name], true
	}
	return Template{}, false
}
