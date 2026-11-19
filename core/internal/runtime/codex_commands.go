package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Ady-JK/paladin/core/internal/agent"
	"github.com/Ady-JK/paladin/core/internal/codex"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

// codexCommand is a slash command typed in a Codex session. They are the
// commands Codex itself has: the core recognizes them in the message, so every
// client gets the same behavior.
type codexCommand struct {
	name string
	args string
}

// codexCommands are the slash commands the core handles; anything else that
// starts with a slash goes to Codex as an ordinary message.
var codexCommands = map[string]bool{"compact": true, "goal": true, "review": true, "feedback": true, "mcp": true, "fast": true}

// parseCodexCommand reads a command from the start of a message.
func parseCodexCommand(message string) (codexCommand, bool) {
	text := strings.TrimSpace(message)
	if !strings.HasPrefix(text, "/") {
		return codexCommand{}, false
	}
	line, rest, _ := strings.Cut(text, "\n")
	name, args, _ := strings.Cut(strings.TrimPrefix(line, "/"), " ")
	name = strings.ToLower(strings.TrimSpace(name))
	if !codexCommands[name] {
		return codexCommand{}, false
	}
	args = strings.TrimSpace(strings.TrimSpace(args) + "\n" + rest)
	return codexCommand{name: name, args: args}, true
}

// local commands answer without a Codex turn of their own.
func (c codexCommand) local() bool {
	return c.name == "goal" || c.name == "feedback" || c.name == "mcp" || c.name == "fast"
}

// reviewTarget maps `/review [base <branch> | commit <sha> | <instructions>]`
// onto Codex's review targets.
func (c codexCommand) reviewTarget() map[string]any {
	fields := strings.Fields(c.args)
	switch {
	case len(fields) == 0:
		return map[string]any{"type": "uncommittedChanges"}
	case len(fields) == 2 && strings.EqualFold(fields[0], "base"):
		return map[string]any{"type": "baseBranch", "branch": fields[1]}
	case len(fields) == 2 && strings.EqualFold(fields[0], "commit"):
		return map[string]any{"type": "commit", "sha": fields[1]}
	}
	return map[string]any{"type": "custom", "instructions": c.args}
}

// localCommand runs a command that needs no turn and shows its answer as the
// assistant's reply.
func (c *codexRun) localCommand(ctx context.Context, cmd codexCommand) error {
	var reply string
	var err error
	switch cmd.name {
	case "goal":
		reply, err = c.goalCommand(ctx, cmd.args)
	case "fast":
		reply, err = c.fastCommand(ctx, cmd.args)
	case "mcp":
		var servers []codex.McpServer
		if servers, err = c.client.McpServers(ctx, c.threadID); err == nil {
			reply = describeMcpServers(servers)
		}
	case "feedback":
		var id string
		if id, err = c.client.UploadFeedback(ctx, c.threadID, cmd.args); err == nil {
			reply = "Feedback sent to OpenAI. Thread ID: `" + id + "`. Share it with OpenAI support."
		}
	}
	if err != nil {
		return err
	}
	c.agentDelta("local", reply)
	c.status = "completed"
	return nil
}

// goalCommand implements /goal: show it, set it, pause, resume or clear it.
func (c *codexRun) goalCommand(ctx context.Context, args string) (string, error) {
	switch strings.ToLower(args) {
	case "":
		goal, err := c.client.GetGoal(ctx, c.threadID)
		if err != nil {
			return "", err
		}
		if goal == nil {
			return "No goal is set. Use `/goal <what done looks like>` to set one.", nil
		}
		return describeGoal(goal), nil
	case "clear":
		if err := c.client.ClearGoal(ctx, c.threadID); err != nil {
			return "", err
		}
		return "Goal cleared.", nil
	case "pause", "resume":
		status := map[string]string{"pause": "paused", "resume": "active"}[strings.ToLower(args)]
		goal, err := c.client.SetGoal(ctx, c.threadID, "", status)
		if err != nil {
			return "", err
		}
		return describeGoal(goal), nil
	}
	goal, err := c.client.SetGoal(ctx, c.threadID, args, "active")
	if err != nil {
		return "", err
	}
	return "Goal set. Codex keeps working until it is met.\n\n" + describeGoal(goal), nil
}

func describeGoal(g *codex.Goal) string {
	if g == nil {
		return "No goal is set."
	}
	used := ""
	if g.TokensUsed > 0 || g.TimeUsedSec > 0 {
		used = fmt.Sprintf(" · %d tokens · %s", g.TokensUsed, (time.Duration(g.TimeUsedSec) * time.Second).String())
	}
	return fmt.Sprintf("**Goal** (%s): %s%s", g.Status, g.Objective, used)
}

// compactionNotice reports a Codex compaction in the way Paladin shows its own.
func (c *codexRun) compactionNotice(phase string) {
	reason := "auto"
	if c.manualCompact {
		reason = "manual"
	}
	if phase == "done" {
		c.flushText(nil)
		// Codex owns the summary and does not expose it on this item. Keep a
		// checkpoint row so live, imported and reopened turns share a divider.
		c.records = append(c.records, transcript.Message{Role: transcript.RoleSystem, CreatedAt: time.Now().UTC(),
			Compaction: &transcript.Compaction{Reason: reason}})
	}
	c.send(agent.ChatChunk{Compaction: &agent.CompactionStatus{Phase: phase, Reason: reason}})
}

// describeMcpServers renders the /mcp answer.
func describeMcpServers(servers []codex.McpServer) string {
	if len(servers) == 0 {
		return "Codex has no MCP servers configured. Add one with `codex mcp add`."
	}
	lines := []string{fmt.Sprintf("**MCP servers** (%d)", len(servers))}
	for _, s := range servers {
		state := mcpState(s)
		tools := fmt.Sprintf("%d tools", s.Tools)
		if s.Tools == 1 {
			tools = "1 tool"
		}
		line := fmt.Sprintf("- `%s` — %s · %s", s.Name, state, tools)
		if s.ToolsError != "" {
			line += " · " + s.ToolsError
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// mcpState puts a server's state in words.
func mcpState(s codex.McpServer) string {
	switch s.Status {
	case "connected":
		return "connected"
	case "starting":
		return "starting"
	case "failed":
		return "failed"
	case "disabled":
		return "disabled"
	case "cancelled":
		return "cancelled"
	case "authenticationRequired":
		return "needs sign-in"
	}
	if s.Auth == "notLoggedIn" {
		return "needs sign-in"
	}
	return "not started yet"
}

// fastCommand implements /fast: show, turn on or turn off Codex's fast mode for
// the model in use.
func (c *codexRun) fastCommand(ctx context.Context, args string) (string, error) {
	models, err := c.client.Models(ctx)
	if err != nil {
		return "", err
	}
	model := c.model
	var info *codex.Model
	for i := range models {
		if models[i].ID == model || (model == "" && models[i].IsDefault) {
			info = &models[i]
		}
	}
	if info == nil || info.FastTier == "" {
		return fmt.Sprintf("%s has no fast mode.", orDefault(model, "This model")), nil
	}
	current := c.r.loadSettings().ServiceTiers[HarnessCodex][info.ID]
	switch strings.ToLower(args) {
	case "on", "off":
		on := strings.ToLower(args) == "on"
		err := c.r.updateSettings(func(s *Settings) {
			if s.ServiceTiers == nil {
				s.ServiceTiers = map[string]map[string]string{}
			}
			if s.ServiceTiers[HarnessCodex] == nil {
				s.ServiceTiers[HarnessCodex] = map[string]string{}
			}
			if on {
				s.ServiceTiers[HarnessCodex][info.ID] = info.FastTier
			} else {
				delete(s.ServiceTiers[HarnessCodex], info.ID)
			}
		})
		if err != nil {
			return "", err
		}
		current = ""
		if on {
			current = info.FastTier
		}
	case "":
	default:
		return "Use `/fast on`, `/fast off`, or `/fast` to see the current setting.", nil
	}
	if current == "" {
		return fmt.Sprintf("Fast mode is off for %s. `/fast on` makes it faster at the cost of more of your plan.", info.ID), nil
	}
	note := orDefault(info.FastNote, "faster, uses more of your plan")
	return fmt.Sprintf("Fast mode is on for %s (%s).", info.ID, note), nil
}
