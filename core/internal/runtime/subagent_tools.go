// subagent_tools.go is the model-facing control surface for child
// agents: spawn_agent, send_message, followup_task, wait_agent,
// interrupt_agent and list_agents. Every call is scoped to the calling
// session's own children; children never get these tools.

package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Ady-JK/paladin/core/internal/jsonx"
	"github.com/Ady-JK/paladin/core/internal/tools"
)

// agentToolNames are the delegation tools; children never get them.
var agentToolNames = []string{"spawn_agent", "send_message", "followup_task", "wait_agent", "interrupt_agent", "list_agents"}

const (
	waitDefault = 60 * time.Second
	waitMin     = time.Second
	waitMax     = 10 * time.Minute
)

// workspaceNote is told to the model with every spawn: children share
// the parent's folder but cannot change it, so they never conflict.
const workspaceNote = "shared, read-only: the child reads the same files as you but cannot write, edit or run mutating commands, so parallel children never conflict over files. Make any changes yourself after reading its result."

func (r *AgentRuntime) registerAgentTools() {
	m := r.subagents
	r.tools.Register(&agentTool{name: "spawn_agent", parallel: true, def: spawnDef(), call: m.spawnCall})
	r.tools.Register(&agentTool{name: "send_message", parallel: true, def: sendDef(), call: m.sendCall})
	r.tools.Register(&agentTool{name: "followup_task", parallel: true, def: followupDef(), call: m.followupCall})
	r.tools.Register(&agentTool{name: "wait_agent", def: waitDef(), call: m.waitCall})
	r.tools.Register(&agentTool{name: "interrupt_agent", parallel: true, def: interruptDef(), call: m.interruptCall})
	r.tools.Register(&agentTool{name: "list_agents", parallel: true, def: listDef(), call: m.listCall})
}

type agentTool struct {
	name     string
	parallel bool
	def      tools.ToolDefinition
	call     func(raw json.RawMessage, ctx *tools.ToolCtx) (any, error)
}

func (t *agentTool) Name() string                     { return t.name }
func (t *agentTool) Definition() tools.ToolDefinition { return t.def }
func (t *agentTool) ParallelSafe() bool               { return t.parallel }

func (t *agentTool) Call(raw json.RawMessage, ctx *tools.ToolCtx) (string, error) {
	if ctx == nil {
		ctx = &tools.ToolCtx{}
	}
	out, err := t.call(raw, ctx)
	if err != nil {
		if te := (*tools.ToolError)(nil); errors.As(err, &te) {
			return "", te
		}
		return "", tools.ArgsError(t.name + ": " + err.Error())
	}
	data, err := jsonx.Marshal(out)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func decodeArgs(name string, raw json.RawMessage, into any) error {
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	// Strict, like the schema's additionalProperties: false, so a
	// misspelled field is an error the model can fix, not a silent no-op.
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return tools.ArgsError(fmt.Sprintf("%s: invalid arguments: %v", name, err))
	}
	return nil
}

func objectSchema(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

var targetProp = map[string]any{"type": "string", "description": "The agent_id (or unique name) returned by spawn_agent."}

func spawnDef() tools.ToolDefinition {
	var kinds []string
	var lines []string
	for _, k := range subagentKinds {
		kinds = append(kinds, k.Name)
		lines = append(lines, "- "+k.Name+": "+k.Description)
	}
	return tools.ToolDefinition{
		Name: "spawn_agent",
		Description: "Start a subagent on a focused task in the background and return its agent_id at once. " +
			"Subagents are read-only (they read files and run inspection commands in your workspace but cannot change anything) " +
			"and cannot spawn agents. Use them for parallel, independent investigation; do quick lookups yourself. " +
			"Their final reply comes back to you automatically when they finish (or call wait_agent). Types:\n" +
			strings.Join(lines, "\n"),
		Parameters: objectSchema(map[string]any{
			"agent_type": map[string]any{"type": "string", "enum": kinds, "description": "Which kind of subagent to start."},
			"task": map[string]any{"type": "string", "description": "The complete assignment. With fork_turns \"none\" the child sees only this, " +
				"so include every fact, path and constraint it needs and say what to report back."},
			"name": map[string]any{"type": "string", "description": "Optional short label, unique among your agents (e.g. \"auth-flow\")."},
			"fork_turns": map[string]any{"type": "string", "enum": []string{ForkNone, ForkAll},
				"description": "\"none\" (default): the child starts fresh with only the task — cheapest and most focused. " +
					"\"all\": the child also gets a copy of this conversation's finished turns (not the current turn) — " +
					"use only when it truly needs that history."},
			"model": map[string]any{"type": "string", "description": "Optional model id from your provider; defaults to your model."},
		}, "agent_type", "task"),
	}
}

func (m *subagentManager) spawnCall(raw json.RawMessage, ctx *tools.ToolCtx) (any, error) {
	var a struct {
		AgentType string `json:"agent_type"`
		Task      string `json:"task"`
		Name      string `json:"name"`
		ForkTurns string `json:"fork_turns"`
		Model     string `json:"model"`
	}
	if err := decodeArgs("spawn_agent", raw, &a); err != nil {
		return nil, err
	}
	kind, ok := findSubagentKind(strings.TrimSpace(a.AgentType))
	if !ok {
		return nil, fmt.Errorf("agent_type must be one of %s, got %q", strings.Join(subagentKindNames(), ", "), a.AgentType)
	}
	task := strings.TrimSpace(a.Task)
	if task == "" {
		return nil, fmt.Errorf("task is required")
	}
	fork := strings.TrimSpace(a.ForkTurns)
	if fork == "" {
		fork = ForkNone
	}
	if fork != ForkNone && fork != ForkAll {
		return nil, fmt.Errorf("fork_turns must be %q or %q, got %q", ForkNone, ForkAll, a.ForkTurns)
	}
	model := strings.TrimSpace(a.Model)
	if model != "" {
		if err := m.checkModel(ctx.SessionID, model); err != nil {
			return nil, err
		}
	}
	info, err := m.spawn(ctx.SessionID, spawnArgs{Kind: kind, Task: task, Name: a.Name, ForkTurns: fork, Model: model, CallID: ctx.CallID})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"agent_id": info.ID, "name": info.Name, "agent_type": info.Type, "status": info.Status,
		"fork_turns": info.ForkTurns, "workspace": workspaceNote,
		"note": "Running in the background. Its result is delivered to you when it finishes; keep working or call wait_agent.",
	}, nil
}

// checkModel accepts only models the parent's provider serves, so a
// child never gains a model its parent could not use.
func (m *subagentManager) checkModel(parentID, model string) error {
	parent, err := m.owner(parentID)
	if err != nil {
		return err
	}
	settings := m.settingsFor(parentID, parent.Meta.Cwd)
	provider := derefString(settings.Provider)
	if provider == "" {
		provider = derefString(parent.Meta.Provider)
	}
	if model == derefString(settings.Model) || model == derefString(parent.Meta.Model) {
		return nil
	}
	known := m.r.store.ModelsFor(provider)
	for _, k := range known {
		if k == model {
			return nil
		}
	}
	if len(known) == 0 {
		return fmt.Errorf("model %q is not known for provider %q; omit model to use yours", model, provider)
	}
	return fmt.Errorf("model %q is not available from provider %q; omit model to use yours", model, provider)
}

func sendDef() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "send_message",
		Description: "Send guidance to one of your subagents while it is running; it reads the message before its next step. Fails if the agent is not running (use followup_task then).",
		Parameters: objectSchema(map[string]any{
			"target":  targetProp,
			"message": map[string]any{"type": "string", "description": "What to tell the agent."},
		}, "target", "message"),
	}
}

func (m *subagentManager) sendCall(raw json.RawMessage, ctx *tools.ToolCtx) (any, error) {
	var a struct {
		Target  string `json:"target"`
		Message string `json:"message"`
	}
	if err := decodeArgs("send_message", raw, &a); err != nil {
		return nil, err
	}
	if strings.TrimSpace(a.Message) == "" {
		return nil, fmt.Errorf("message is required")
	}
	info, err := m.message(ctx.SessionID, a.Target, strings.TrimSpace(a.Message))
	if err != nil {
		return nil, err
	}
	return map[string]any{"agent_id": info.ID, "name": info.Name, "status": info.Status, "delivered": true}, nil
}

func followupDef() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name: "followup_task",
		Description: "Give one of your subagents another task. It keeps its own conversation, so refer to its earlier work freely. " +
			"If it is still busy the task runs after its current one. The result is delivered like a spawn's.",
		Parameters: objectSchema(map[string]any{
			"target":  targetProp,
			"message": map[string]any{"type": "string", "description": "The new task or question."},
		}, "target", "message"),
	}
}

func (m *subagentManager) followupCall(raw json.RawMessage, ctx *tools.ToolCtx) (any, error) {
	var a struct {
		Target  string `json:"target"`
		Message string `json:"message"`
	}
	if err := decodeArgs("followup_task", raw, &a); err != nil {
		return nil, err
	}
	if strings.TrimSpace(a.Message) == "" {
		return nil, fmt.Errorf("message is required")
	}
	info, err := m.followup(ctx.SessionID, a.Target, strings.TrimSpace(a.Message))
	if err != nil {
		return nil, err
	}
	return map[string]any{"agent_id": info.ID, "name": info.Name, "status": info.Status}, nil
}

func waitDef() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name: "wait_agent",
		Description: "Wait until one of your subagents finishes (or the timeout passes) and return statuses plus any new results. " +
			"Results are delivered once: a result returned here will not be sent again. Only wait when you have nothing else useful to do.",
		Parameters: objectSchema(map[string]any{
			"targets": map[string]any{"type": "array", "items": map[string]any{"type": "string"},
				"description": "agent_ids or names to wait for; omit for all of your agents."},
			"timeout_ms": map[string]any{"type": "integer", "minimum": waitMin.Milliseconds(), "maximum": waitMax.Milliseconds(),
				"description": fmt.Sprintf("How long to wait (default %d).", waitDefault.Milliseconds())},
		}),
	}
}

func (m *subagentManager) waitCall(raw json.RawMessage, ctx *tools.ToolCtx) (any, error) {
	var a struct {
		Targets   []string `json:"targets"`
		TimeoutMs *int64   `json:"timeout_ms"`
	}
	if err := decodeArgs("wait_agent", raw, &a); err != nil {
		return nil, err
	}
	timeout := waitDefault
	if a.TimeoutMs != nil {
		timeout = min(max(time.Duration(*a.TimeoutMs)*time.Millisecond, waitMin), waitMax)
	}
	parent := ctx.Cancel
	if parent == nil {
		parent = context.Background()
	}
	res, err := m.wait(parent, ctx.SessionID, a.Targets, timeout)
	if err != nil {
		return nil, err
	}
	agents := make([]map[string]any, 0, len(res.Agents))
	for _, info := range res.Agents {
		agents = append(agents, agentView(info, true))
	}
	out := map[string]any{"agents": agents, "timed_out": res.TimedOut}
	if len(agents) == 0 {
		out["note"] = "You have no subagents."
	}
	return out, nil
}

func interruptDef() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "interrupt_agent",
		Description: "Stop one of your subagents: cancels its running turn and any queued follow-ups. Its transcript is kept and it can be given a followup_task later.",
		Parameters:  objectSchema(map[string]any{"target": targetProp}, "target"),
	}
}

func (m *subagentManager) interruptCall(raw json.RawMessage, ctx *tools.ToolCtx) (any, error) {
	var a struct {
		Target string `json:"target"`
	}
	if err := decodeArgs("interrupt_agent", raw, &a); err != nil {
		return nil, err
	}
	info, stopped, err := m.interrupt(ctx.SessionID, a.Target)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"agent_id": info.ID, "name": info.Name, "status": info.Status, "interrupted": stopped}
	if !stopped {
		out["note"] = "The agent was not running; nothing to stop."
	}
	return out, nil
}

func listDef() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "list_agents",
		Description: "List your subagents with their status and a preview of their latest result.",
		Parameters:  objectSchema(map[string]any{}),
	}
}

func (m *subagentManager) listCall(raw json.RawMessage, ctx *tools.ToolCtx) (any, error) {
	if err := decodeArgs("list_agents", raw, &struct{}{}); err != nil {
		return nil, err
	}
	infos, err := m.list(ctx.SessionID)
	if err != nil {
		return nil, err
	}
	agents := make([]map[string]any, 0, len(infos))
	for _, info := range infos {
		agents = append(agents, agentView(info, false))
	}
	return map[string]any{"agents": agents}, nil
}

// agentView is one agent in a tool result; full puts the whole
// (clipped) result in, otherwise a short preview.
func agentView(info AgentInfo, full bool) map[string]any {
	v := map[string]any{"agent_id": info.ID, "name": info.Name, "agent_type": info.Type, "status": info.Status}
	if info.Error != "" {
		v["error"] = info.Error
	}
	switch {
	case info.Result == "":
	case full:
		v["result"] = clip(info.Result, resultLimit)
	default:
		v["result_preview"] = clip(info.Result, 300)
	}
	// A delivered result's id lands in the parent transcript with this
	// tool result; a restart uses it to avoid delivering it twice.
	if full && info.CompletionID != "" {
		v["completion_id"] = info.CompletionID
	}
	return v
}
