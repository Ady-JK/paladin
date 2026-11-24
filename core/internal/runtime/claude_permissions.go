package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/Ady-JK/paladin/core/internal/agent"
	"github.com/Ady-JK/paladin/core/internal/claude"
	"github.com/Ady-JK/paladin/core/internal/harness/claudeadapter"
	"github.com/Ady-JK/paladin/core/internal/permission"
	"github.com/Ady-JK/paladin/core/internal/tools"
	"github.com/Ady-JK/paladin/core/internal/transcript"
	"github.com/google/uuid"
)

func (c *claudeRun) authorize(ctx context.Context, id, name string, input claude.Raw) permission.Auth {
	// Native Claude tools execute outside Paladin's file tool validation, so
	// normalize and validate their paths at this boundary instead.
	normalized := claude.Raw{}
	for k, v := range input {
		normalized[k] = v
	}
	if name == "Glob" || name == "Grep" {
		path := input.Str("path")
		if path == "" {
			path = c.req.Cwd
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(c.req.Cwd, path)
		}
		normalized["path"], _ = json.Marshal(filepath.Clean(path))
	}
	if name == "Read" || name == "Write" || name == "Edit" {
		path := input.Str("file_path")
		if strings.TrimSpace(path) == "" || strings.ContainsRune(path, 0) {
			return permission.Auth{Content: "invalid file path"}
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(c.req.Cwd, path)
		}
		normalized["file_path"], _ = json.Marshal(filepath.Clean(path))
	}
	card := claudeadapter.Tool(id, name, normalized)
	plan := c.req.Mode == "plan"
	gate := permission.Gate{Workspace: c.req.Cwd, SessionID: c.req.SessionID, Broker: c.r.broker, Plan: plan, MCP: func(tool string) (tools.MCPInfo, bool) {
		parts := strings.SplitN(strings.TrimPrefix(tool, "mcp__"), "__", 2)
		if !strings.HasPrefix(tool, "mcp__") || len(parts) != 2 {
			return tools.MCPInfo{}, false
		}
		return tools.MCPInfo{Server: parts[0], Tool: parts[1], Destructive: true}, true
	}}
	if plan && strings.HasPrefix(card.Name, "claude_") {
		switch name {
		case "TaskCreate", "TaskUpdate", "TaskGet", "TaskList", "TodoRead", "Skill", "EnterPlanMode", "ExitPlanMode":
		default:
			return permission.Auth{Content: "Plan mode blocks this Claude tool; describe the change in the plan instead"}
		}
	}
	return gate.Authorize(ctx, card.ID, card.Name, json.RawMessage(card.Args), func(a permission.Approval) { c.send(agent.ChatChunk{Approval: &a}) })
}
func (c *claudeRun) handleControl(ctx context.Context, req claude.Raw) (any, error) {
	switch req.Str("subtype") {
	case "hook_callback":
		if req.Str("callback_id") != "paladin-permission" {
			return nil, fmt.Errorf("unknown Claude hook callback %q", req.Str("callback_id"))
		}
		input := req.Obj("input")
		name := input.Str("tool_name")
		id := req.Str("tool_use_id")
		if id == "" {
			id = input.Str("tool_use_id")
		}
		if id == "" {
			id = uuid.NewString()
		}
		c.mu.Lock()
		_, alreadyShown := c.p.Tools[id]
		card := claudeadapter.Tool(id, name, input.Obj("tool_input"))
		c.p.Tools[id] = card
		c.p.NativeTools[id] = name
		err := c.checkpointLocked()
		c.mu.Unlock()
		if !alreadyShown {
			c.send(agent.ChatChunk{Tool: &agent.ToolActivity{ID: id, Name: card.Name, Arguments: card.Args, Status: "running"}})
		}
		if err != nil {
			return nil, err
		}
		if name == "AskUserQuestion" {
			return map[string]any{}, nil
		}
		auth := c.authorize(ctx, id, name, input.Obj("tool_input"))
		if !auth.Run {
			return map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": "PreToolUse", "permissionDecision": "deny", "permissionDecisionReason": auth.Content}}, nil
		}
		return map[string]any{}, nil
	case "can_use_tool":
		if req.Str("tool_name") == "AskUserQuestion" {
			return c.questions(ctx, req)
		}
		id := req.Str("tool_use_id")
		if id == "" {
			id = uuid.NewString()
		}
		auth := c.authorize(ctx, id, req.Str("tool_name"), req.Obj("input"))
		if !auth.Run {
			return map[string]any{"behavior": "deny", "message": auth.Content}, nil
		}
		// Outside plan mode, approve what passed Paladin's guard so the user is
		// not prompted for commands one at a time.
		if c.req.Mode != "plan" {
			return map[string]any{"behavior": "allow", "updatedInput": req.Obj("input")}, nil
		}
		// In plan mode, prompt the user.
		card := claudeadapter.Tool(id, req.Str("tool_name"), req.Obj("input"))
		a := permission.Approval{ID: id, Tool: card.Name, Kind: permission.KindWrite, Path: req.Obj("input").Str("file_path"), Reasons: reasonList(req.Str("decision_reason"), "Claude Code asks for permission"), Choices: []string{permission.ReplyOnce, permission.ReplyDeny}, State: permission.StatePending}
		if card.Name == "shell" {
			a.Kind = permission.KindShell
			a.Command = req.Obj("input").Str("command")
		}
		ch := c.r.broker.Open(c.req.SessionID, id)
		c.send(agent.ChatChunk{Approval: &a})
		reply, err := c.r.broker.Wait(ctx, c.req.SessionID, id, ch)
		a.State = permission.StateDenied
		a.Decision = reply
		if err == nil && reply != permission.ReplyDeny {
			a.State = permission.StateApproved
		}
		c.send(agent.ChatChunk{Approval: &a})
		if a.State == permission.StateApproved {
			return map[string]any{"behavior": "allow", "updatedInput": req.Obj("input")}, nil
		}
		return map[string]any{"behavior": "deny", "message": "Denied or interrupted by the user"}, nil
	default:
		return nil, fmt.Errorf("unsupported Claude control request %q", req.Str("subtype"))
	}
}
func (c *claudeRun) checkpointLocked() error {
	if c.journal == nil {
		return nil
	}
	rows := append([]transcript.Message{}, c.userRows...)
	rows = append(rows, c.p.Snapshot()...)
	if err := c.journal.Checkpoint(rows, ""); err != nil {
		return fmt.Errorf("checkpoint Claude turn: %w", err)
	}
	c.lastCheckpoint = time.Now()
	return nil
}
