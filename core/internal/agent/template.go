// template.go is the composition layer. A Template is a
// reusable blueprint (prompt and tool set); Build resolves it
// against runtime dependencies into a ready-to-run Agent.

package agent

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/Ady-JK/paladin/core/internal/custom"
	"github.com/Ady-JK/paladin/core/internal/hooks"
	"github.com/Ady-JK/paladin/core/internal/inference"
	"github.com/Ady-JK/paladin/core/internal/permission"
	"github.com/Ady-JK/paladin/core/internal/prompt"
	"github.com/Ady-JK/paladin/core/internal/skills"
	"github.com/Ady-JK/paladin/core/internal/tools"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

// Template is the reusable blueprint for one kind of agent. The zero
// value is valid: it uses the runtime's default prompt, all registered
// tools.
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
	// ReadOnly drops the file writers and swaps in the inspection-only
	// shell, as plan mode does.
	ReadOnly bool `json:"readOnly,omitempty"`
	// Prompt is appended to the system prompt (a subagent's role).
	Prompt string `json:"prompt,omitempty"`
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
	// Tools are command-only tools (tools.CommandTool) for this turn.
	Tools []string
}

// CommandSource resolves `/name args` prompt commands for a session.
type CommandSource interface {
	LoadCommand(cwd, sessionID, name, args string) (Command, error)
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
	PersistGate func(persist func() error)
	// Gate, when set, authorizes and sandboxes each tool call.
	Gate ToolGate
	// Hooks runs the user's hook scripts; nil runs none. SessionStart is
	// "startup" or "resume" when this turn opens the session.
	Hooks        *hooks.Engine
	SessionStart string
	// Live, when set, receives a copy of the conversation before each
	// model round (see Agent.live).
	Live func([]inference.LlmMessage)
	// ContextChoice supplies changes made while a turn is running.
	ContextChoice func() (int64, float64)
}

// Build resolves a Template into an Agent. Everything the loop needs
// (session, history, provider, tool subset, prompt, limits) is fixed
// here, before any model call happens.
func (t Template) Build(d Deps, req Request) (*Agent, error) {
	command, mode, err := t.validateBuild(d, req)
	if err != nil {
		return nil, err
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

	input := buildInput{message: req.Message, display: req.Display, mode: mode}
	if err := input.applyCommand(d.Commands, sess, req, command); err != nil {
		return nil, err
	}
	if err := input.applySkill(d.Skills, sess, req); err != nil {
		return nil, err
	}

	system := ComposeSystem(d.Prompts.SystemPrompt(strVal(req.SystemPrompt)), sess, d.Sections)
	registry, shell, err := t.buildRegistry(d, input.commandTools)
	if err != nil {
		return nil, err
	}
	gate := sessionGate(d.Gate, sess)
	if input.mode == ModePlan || t.ReadOnly {
		registry, shell = planRegistry(registry, shell)
		if !t.ReadOnly {
			system += "\n\n" + custom.Text("prompt.plan-mode")
		}
		gate = planGate(gate)
	}
	if role := strings.TrimSpace(t.Prompt); role != "" {
		system += "\n\n" + role
	}

	return newAgent(runConfig{
		session:       sess,
		provider:      provider,
		window:        req.ContextWindow,
		compactPct:    req.CompactPercent,
		contextChoice: d.ContextChoice,
		tools:         registry,
		shell:         shell,
		toolDefs:      toolDefinitions(registry),
		history:       history,
		system:        system,
		ctx:           d.Context,
		cancel:        d.Cancel,
		tx:            d.Tx,
		done:          d.Done,
		persistGate:   d.PersistGate,
		gate:          gate,
		steer:         req.Steer,
		budget:        req.Budget,
		notices:       req.Notices,
		format:        req.FormatOnWrite,
		web:           req.Web,
		diagnose:      req.Diagnose,
		warm:          req.Warm,
		ledgers:       req.SpendLedgers,
		message:       input.message,
		display:       input.display,
		attachments:   req.Attachments,
		resume:        req.Resume,
		compactOnly:   req.Compact,

		hooks:          d.Hooks,
		sessionStart:   d.SessionStart,
		live:           d.Live,
		permissionMode: req.Permissions,
	}), nil
}

func (t Template) validateBuild(d Deps, req Request) (command, mode string, err error) {
	if strings.TrimSpace(req.SessionID) == "" {
		return "", "", fmt.Errorf("sessionId is required: create one with openSession first")
	}
	command = strings.TrimSpace(strVal(req.Command))
	if strings.TrimSpace(req.Message) == "" && !req.Resume && !req.Compact && command == "" {
		return "", "", fmt.Errorf("message is required")
	}
	mode = strings.TrimSpace(req.Mode)
	if mode != "" && mode != ModeBuild && mode != ModePlan {
		return "", "", fmt.Errorf("mode must be %q or %q, got %q", ModeBuild, ModePlan, mode)
	}
	if req.Compact && (req.Resume || command != "" || strVal(req.Skill) != "") {
		return "", "", fmt.Errorf("compact cannot be combined with resume, a command or a skill")
	}
	if d.Sessions == nil || d.Providers == nil || d.Prompts == nil {
		return "", "", fmt.Errorf("agent template %q: missing runtime dependencies", t.Name)
	}
	if d.Tools == nil {
		return "", "", fmt.Errorf("agent template %q: tool registry is required", t.Name)
	}
	return command, mode, nil
}

type buildInput struct {
	message      string
	display      string
	mode         string
	commandTools []string
}

func (input *buildInput) applyCommand(source CommandSource, sess transcript.Session, req Request, command string) error {
	if command == "" {
		return nil
	}
	if req.Resume {
		return fmt.Errorf("command %q cannot be used with resume", command)
	}
	if source == nil {
		return fmt.Errorf("command %q is not available: the runtime has no command source", command)
	}
	cmd, err := source.LoadCommand(sess.Meta.Cwd, sess.Meta.SessionID, command, strings.TrimSpace(req.Message))
	if err != nil {
		return err
	}
	if input.display == "" {
		input.display = strings.TrimSpace("/" + cmd.Name + " " + strings.TrimSpace(req.Message))
	}
	input.message = cmd.Prompt
	if cmd.Mode != "" && input.mode == "" {
		input.mode = cmd.Mode
	}
	for _, name := range cmd.Tools {
		if _, ok := tools.CommandTool(name); !ok {
			return fmt.Errorf("command /%s asks for unknown tool %q", cmd.Name, name)
		}
	}
	input.commandTools = cmd.Tools
	return nil
}

func (input *buildInput) applySkill(source SkillSource, sess transcript.Session, req Request) error {
	name := strings.TrimSpace(strVal(req.Skill))
	if name == "" {
		return nil
	}
	if req.Resume {
		return fmt.Errorf("skill %q cannot be used with resume", name)
	}
	if source == nil {
		return fmt.Errorf("skill %q is not available: the runtime has no skill source", name)
	}
	var skill Skill
	var err error
	if contextual, ok := source.(interface {
		LoadSkillForSession(cwd, sessionID, name string) (Skill, error)
	}); ok {
		skill, err = contextual.LoadSkillForSession(sess.Meta.Cwd, sess.Meta.SessionID, name)
	} else {
		skill, err = source.LoadSkill(sess.Meta.Cwd, name)
	}
	if err != nil {
		return err
	}
	if input.display == "" {
		input.display = req.Message
	}
	input.message = FormatSkillMessage(input.message, skill.Name, skill.Content)
	return nil
}

func (t Template) buildRegistry(d Deps, commandTools []string) (*tools.Registry, *tools.ShellTool, error) {
	registry, shell := d.Tools, d.Shell
	if len(t.Tools) > 0 {
		subset, err := d.Tools.Subset(t.Tools)
		if err != nil {
			return nil, nil, fmt.Errorf("agent template %q: %w", t.Name, err)
		}
		registry, shell = subset, nil
		if raw, ok := subset.Get("shell"); ok {
			shell, _ = raw.(*tools.ShellTool)
		}
	}
	for _, name := range commandTools {
		tool, _ := tools.CommandTool(name)
		registry = registry.With(tool)
	}
	return registry, shell, nil
}

func sessionGate(gate ToolGate, sess transcript.Session) ToolGate {
	// The session may read its own folder without mutating the caller's gate.
	if original, ok := gate.(*permission.Gate); ok && original != nil && sess.Dir != "" {
		owned := *original
		owned.Readable = append(slices.Clip(original.Readable), sess.Dir)
		return &owned
	}
	return gate
}

func planGate(gate ToolGate) ToolGate {
	if original, ok := gate.(*permission.Gate); ok && original != nil {
		planned := *original
		planned.Plan = true
		return &planned
	}
	return gate
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
	for _, def := range tools.Described(registry.Definitions()) {
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

// planRegistry is the read-only view of a registry: no file writers, no
// extension tools (local code that may change anything) and a shell that
// refuses anything but inspection commands. MCP tools stay; the
// plan-mode gate asks for each call. The real shell stays as the agent's
// job-notice source.
func planRegistry(r *tools.Registry, shell *tools.ShellTool) (*tools.Registry, *tools.ShellTool) {
	out := tools.NewRegistry()
	for _, name := range r.Names() {
		if planWriteTools[name] || r.IsExtension(name) {
			continue
		}
		t, _ := r.Get(name)
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
