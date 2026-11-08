// plugininvoke.go runs a tool on behalf of a plugin panel. The call goes
// through the same permission gate as a tool the agent asks for, so a
// panel can never silently write a file or run a command. When the gate
// asks, the approval is published as a plugin/approval event and the
// request waits for AnswerApproval.

package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Ady-JK/paladin/core/internal/permission"
	"github.com/Ady-JK/paladin/core/internal/sandbox"
	"github.com/Ady-JK/paladin/core/internal/tools"
)

// PluginEvent is a plugin-panel event that is not tied to a turn.
type PluginEvent struct {
	Event     string               `json:"event"`
	SessionID string               `json:"sessionId"`
	Approval  *permission.Approval `json:"approval,omitempty"`
}

// PluginToolResult is one tool's result for a plugin panel. Content is
// the same text the agent would see; IsError marks a failed call.
type PluginToolResult struct {
	Content string `json:"content"`
	IsError bool   `json:"isError"`
}

type pluginSink struct{ fn func(PluginEvent) }

// SetPluginSink receives plugin-panel events.
func (r *AgentRuntime) SetPluginSink(fn func(PluginEvent)) { r.pluginSink.Store(&pluginSink{fn: fn}) }

func (r *AgentRuntime) emitPlugin(e PluginEvent) {
	if p := r.pluginSink.Load(); p != nil && p.fn != nil {
		p.fn(e)
	}
}

// PluginInvoke runs one tool for a panel. The caller (Electron main) has
// already checked the view's declared permissions; the backend repeats
// the extension-ownership check so a stale or buggy caller cannot reach
// another plugin's tools unless the view is destructive.
func (r *AgentRuntime) PluginInvoke(sessionID, cwd, plugin, view, name string, args json.RawMessage, destructive bool) (PluginToolResult, error) {
	sessionID = strings.TrimSpace(sessionID)
	plugin = strings.TrimSpace(plugin)
	view = strings.TrimSpace(view)
	name = strings.TrimSpace(name)
	if sessionID == "" || plugin == "" || view == "" || name == "" {
		return PluginToolResult{}, fmt.Errorf("pluginInvoke needs sessionId, plugin, view and tool")
	}
	var workspace, project, sessionDir string
	if sessionID == "__paladin_plugin_ui__" {
		if !filepath.IsAbs(cwd) {
			return PluginToolResult{}, fmt.Errorf("standalone plugin needs an absolute workspace")
		}
		info, err := os.Stat(cwd)
		if err != nil || !info.IsDir() {
			return PluginToolResult{}, fmt.Errorf("standalone plugin workspace is unavailable")
		}
		workspace, project = filepath.Clean(cwd), filepath.Clean(cwd)
		sessionDir = filepath.Join(r.dataDir, "plugin-ui")
		if err := os.MkdirAll(sessionDir, 0o700); err != nil {
			return PluginToolResult{}, err
		}
	} else {
		sess, err := r.transcripts.Load(cwd, sessionID)
		if err != nil {
			return PluginToolResult{}, err
		}
		workspace = resolveCwd(r, cwd)
		if sess.Meta.Cwd != "" {
			workspace = sess.Meta.Cwd
		}
		project = workspace
		if p := sess.Meta.ProjectCwd(); p != "" {
			project = p
		}
		sessionDir = sess.Dir
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	extra := r.mcpTools(ctx, project)
	extensionTools, extensionErrors := r.extensions.Load(ctx)
	for _, e := range extensionErrors {
		fmt.Fprintf(os.Stderr, "extension: %v\n", e)
	}
	registry := r.tools.With(append(extra, extensionTools...)...)
	if !strings.HasPrefix(name, "extension__") || !strings.HasPrefix(name, "extension__"+plugin+"__") {
		if strings.HasPrefix(name, "extension__") && !destructive {
			return PluginToolResult{}, fmt.Errorf("plugin %s may not call another plugin's tool %q", plugin, name)
		}
	}
	if _, ok := registry.Get(name); !ok {
		return PluginToolResult{}, fmt.Errorf("unknown tool %q", name)
	}

	call := &tools.ToolCall{ID: fmt.Sprintf("plugin-%d", time.Now().UnixNano()), Name: name, Arguments: string(args)}
	preparedName, prepared, ok := registry.Prepare(call)
	if !ok {
		preparedName, prepared = name, args
	}
	gate := &permission.Gate{
		Preset:      permission.Auto,
		Workspace:   workspace,
		SessionID:   sessionID,
		Store:       r.perms,
		Broker:      r.broker,
		SandboxKind: sandbox.Kind(),
		MCP:         registry.MCPInfo,
		Extension:   extensionRef(registry),
	}
	auth := gate.Authorize(ctx, call.ID, preparedName, prepared, func(ap permission.Approval) {
		r.emitPlugin(PluginEvent{Event: "plugin/approval", SessionID: sessionID, Approval: &ap})
	})
	if !auth.Run {
		return PluginToolResult{Content: auth.Content, IsError: true}, nil
	}
	result := registry.Call(call, &tools.ToolCtx{
		Workspace:  workspace,
		Project:    project,
		SessionDir: sessionDir,
		SessionID:  sessionID,
		Cancel:     ctx,
		Sandbox:    auth.Sandbox,
	})
	return PluginToolResult{Content: result.Content, IsError: result.IsError}, nil
}
