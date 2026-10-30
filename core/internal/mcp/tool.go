package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Ady-JK/paladin/core/internal/tools"
)

// mcpTool exposes one server tool to the agent.
type mcpTool struct {
	srv  *server
	tool Tool
	name string
}

func (t *mcpTool) Name() string { return t.name }

func (t *mcpTool) Definition() tools.ToolDefinition {
	desc := strings.TrimSpace(t.tool.Description)
	if desc == "" {
		desc = strings.TrimSpace(t.tool.Title)
	}
	if desc == "" {
		desc = fmt.Sprintf("Tool %q from MCP server %q.", t.tool.Name, t.srv.key.name)
	}
	return tools.ToolDefinition{Name: t.name, Description: desc, Parameters: sanitizeSchema(t.tool.InputSchema)}
}

func (t *mcpTool) MCPInfo() tools.MCPInfo {
	return tools.MCPInfo{
		Server:      t.srv.key.name,
		Tool:        t.tool.Name,
		ReadOnly:    t.tool.Annotations.ReadOnly(),
		Destructive: t.tool.Annotations.Destructive(),
	}
}

// Call runs the tool. A call that dies mid-flight is never retried: the
// server may already have acted on it.
func (t *mcpTool) Call(args json.RawMessage, tc *tools.ToolCtx) (string, error) {
	parent := context.Background()
	if tc != nil && tc.Cancel != nil {
		parent = tc.Cancel
	}
	c, err := t.srv.acquire(parent)
	if err != nil {
		return "", failure(t.errText(parent, err, 0))
	}
	timeout := t.srv.cfg.ToolTimeout()
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	res, err := c.CallTool(ctx, t.tool.Name, args)
	if err != nil {
		return "", failure(t.errText(parent, err, timeout))
	}
	out := renderResult(res)
	if res.IsError {
		return "", tools.WithContent(out, truncate(out, 300))
	}
	return out, nil
}

func (t *mcpTool) errText(parent context.Context, err error, timeout time.Duration) string {
	switch {
	case parent.Err() != nil:
		return "cancelled"
	case timeout > 0 && errors.Is(err, context.DeadlineExceeded):
		return fmt.Sprintf("MCP tool %s timed out after %v", t.tool.Name, timeout)
	}
	return fmt.Sprintf("MCP server %q: %v", t.srv.key.name, err)
}

func failure(msg string) error {
	content, _ := json.Marshal(map[string]string{"error": msg})
	return tools.WithContent(string(content), msg)
}
