// template.go is the composition layer. A Template is a
// reusable blueprint (prompt, tool set, round cap); Build resolves it
// against runtime dependencies into a ready-to-run Agent.

package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/Ady-JK/paladin/core/internal/hooks"
	"github.com/Ady-JK/paladin/core/internal/inference"
	"github.com/Ady-JK/paladin/core/internal/prompt"
	"github.com/Ady-JK/paladin/core/internal/skills"
	"github.com/Ady-JK/paladin/core/internal/tools"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

// Template is the reusable blueprint for one kind of agent. The zero
// value is valid: it uses the runtime's default prompt, all registered
// tools, and the runtime round cap.
//
// The prompt itself is not part of the template: the agent core reads
// SYSTEM_PROMPT.md and TOOL_GUIDANCE.md from the config dir at turn
// time (the settings UI edits them), and only an explicit per-request
// override beats the files.
type Template struct {
	// Name identifies the template in a Registry.
	Name string `json:"name"`
	// Tools selects a subset by name. Empty means every tool in the
	// runtime registry.
	Tools []string `json:"tools,omitempty"`
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

// SkillSource loads one skill's content for injection into the turn.
type SkillSource interface {
	LoadSkill(cwd, name string) (Skill, error)
}

// SectionSource supplies extra system-prompt sections for a session:
// project instructions (AGENTS.md), the skill catalog, and the frozen
// memory snapshot. Empty strings are skipped; order is kept so the
// prompt prefix stays cache-stable across turns.
type SectionSource interface {
	SystemSections(sess transcript.Session) []string
}

// Command is a resolved prompt command: its expanded prompt and the
// mode it asks for ("" keeps the request's mode).
type Command struct {
	Name   string
	Prompt string
	Mode   string
}

// CommandSource resolves `/name args` prompt commands.
type CommandSource interface {
	LoadCommand(cwd, name, args string) (Command, error)
}

// PromptSource supplies the runtime default prompt. The file content
// is authoritative; only an explicit per-request override beats it.
// Tool guidance and examples are compiled per-OS inside prompt.
type PromptSource interface {
	SystemPrompt(override string) string
}

// Deps is the runtime-owned set of collaborators Build needs. Keeping
// them behind interfaces lets a turn be built without a live runtime.
// Skills is optional: a nil source means no skill lookup, and a Request
// naming a skill then fails instead of silently dropping it.
type Deps struct {
	Sessions  SessionSource
	Providers ProviderSource
	Prompts   PromptSource
	Skills    SkillSource
	Sections  SectionSource
	Commands  CommandSource
	Tools     *tools.Registry
	Shell     *tools.ShellTool
	Context   context.Context
	Cancel    context.CancelFunc
	Tx        chan<- ChatChunk
	Done      <-chan struct{}
	// PersistGate, when set, wraps the end-of-turn transcript write.
	PersistGate func(persist func())
	// Gate, when set, authorizes and sandboxes each tool call.
	Gate ToolGate
	// Steps, when set, records each file-changing tool run for undo.
	Steps StepRecorder
	// Hooks runs the user's hook scripts; nil runs none. SessionStart is
	// "startup" or "resume" when this turn opens the session.
	Hooks        *hooks.Engine
	SessionStart string
	// Live, when set, receives a copy of the conversation before each
	// model round (see Agent.live).
	Live func([]inference.LlmMessage)
}

// Build resolves a Template into an Agent. Everything the loop needs
// (session, history, provider, tool subset, prompt, limits) is fixed
// here, before any model call happens.
func (t Template) Build(d Deps, req Request) (*Agent, error) {
	if strings.TrimSpace(req.SessionID) == "" {
		return nil, fmt.Errorf("sessionId is required: create one with openSession first")
	}
	command := strings.TrimSpace(strVal(req.Command))
	if strings.TrimSpace(req.Message) == "" && !req.Resume && !req.Compact && command == "" {
		return nil, fmt.Errorf("message is required")
	}
	mode := strings.TrimSpace(req.Mode)
	if mode != "" && mode != ModeBuild && mode != ModePlan {
		return nil, fmt.Errorf("mode must be %q or %q, got %q", ModeBuild, ModePlan, mode)
	}
	if req.Compact && (req.Resume || command != "" || strVal(req.Skill) != "") {
		return nil, fmt.Errorf("compact cannot be combined with resume, a command or a skill")
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

	message := req.Message
	display := req.Display
	if command != "" {
		if req.Resume {
			return nil, fmt.Errorf("command %q cannot be used with resume", command)
		}
		if d.Commands == nil {
			return nil, fmt.Errorf("command %q is not available: the runtime has no command source", command)
		}
		cmd, err := d.Commands.LoadCommand(sess.Meta.Cwd, command, strings.TrimSpace(req.Message))
		if err != nil {
			return nil, err
		}
		if display == "" {
			display = strings.TrimSpace("/" + cmd.Name + " " + strings.TrimSpace(req.Message))
		}
		message = cmd.Prompt
		if cmd.Mode != "" && mode == "" {
			mode = cmd.Mode
		}
	}
	if name := strings.TrimSpace(strVal(req.Skill)); name != "" {
		if req.Resume {
			return nil, fmt.Errorf("skill %q cannot be used with resume", name)
		}
		if d.Skills == nil {
			return nil, fmt.Errorf("skill %q is not available: the runtime has no skill source", name)
		}
		skill, err := d.Skills.LoadSkill(sess.Meta.Cwd, name)
		if err != nil {
			return nil, err
		}
		if display == "" {
			display = req.Message
		}
		message = FormatSkillMessage(message, skill.Name, skill.Content)
	}

	system := ComposeSystem(d.Prompts.SystemPrompt(strVal(req.SystemPrompt)), sess, d.Sections)

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

	if mode == ModePlan {
		registry, shell = planRegistry(registry, shell)
		system += "\n\n" + prompt.PlanMode
	}

	return newAgent(runConfig{
		session:     sess,
		provider:    provider,
		tools:       registry,
		shell:       shell,
		toolDefs:    toolDefinitions(registry),
		history:     history,
		system:      system,
		ctx:         d.Context,
		cancel:      d.Cancel,
		tx:          d.Tx,
		done:        d.Done,
		persistGate: d.PersistGate,
		gate:        d.Gate,
		steps:       d.Steps,
		steer:       req.Steer,
		budget:      req.Budget,
		message:     message,
		display:     display,
		resume:      req.Resume,
		compactOnly: req.Compact,

		hooks:          d.Hooks,
		sessionStart:   d.SessionStart,
		live:           d.Live,
		permissionMode: req.Permissions,
	}), nil
}

// Skill is the injected skill content for a turn: the name the
// composer attached plus the SKILL.md text the model sees. It mirrors
// skills.Skill without importing the package into the wire contract.
type Skill struct {
	Name    string
	Content string
}

// FormatSkillMessage builds the model-facing user message: the typed
// text plus the skill content under a "Use this skill" banner. It
// delegates to the skills package so the banner text stays identical
// everywhere the expansion happens.
func FormatSkillMessage(message, name, content string) string {
	return skills.Inject(message, skills.Skill{Name: name, Content: content})
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

// planWriteTools are dropped in plan mode.
var planWriteTools = map[string]bool{"write": true, "edit": true}

// planRegistry is the read-only view of a registry: no file writers,
// no MCP tools without a readOnly hint,
// and a shell that refuses anything but inspection commands. The real
// shell stays as the agent's job-notice source.
func planRegistry(r *tools.Registry, shell *tools.ShellTool) (*tools.Registry, *tools.ShellTool) {
	out := tools.NewRegistry()
	for _, name := range r.Names() {
		if planWriteTools[name] {
			continue
		}
		t, _ := r.Get(name)
		if m, ok := t.(tools.MCPTool); ok && !m.MCPInfo().ReadOnly {
			continue
		}
		if name == "shell" {
			sh, ok := t.(*tools.ShellTool)
			if !ok {
				continue
			}
			shell = sh
			out.Register(tools.NewPlanShell(sh))
			continue
		}
		out.Register(t)
	}
	return out, shell
}

// ComposeSystem is a session's full system prompt: the base prompt with
// tool guidance and environment, then the runtime's sections
// (instructions, skills, MCP).
func ComposeSystem(base string, sess transcript.Session, sections SectionSource) string {
	system := prompt.ToolSystem(base, sess.Meta.Cwd)
	if sections != nil {
		for _, section := range sections.SystemSections(sess) {
			if section = strings.TrimSpace(section); section != "" {
				system += "\n\n" + section
			}
		}
	}
	return system
}
