package runtime

import (
	"context"
	"strings"
	"sync/atomic"

	"github.com/Ady-JK/paladin/core/internal/mcp"
	"github.com/Ady-JK/paladin/core/internal/tools"
)

// MCPEvent is the mcp/status event: one server's new state.
type MCPEvent struct {
	Event  string           `json:"event"`
	Server mcp.ServerStatus `json:"server"`
}

// MCPOverview is everything the MCP settings page shows for a folder.
type MCPOverview struct {
	Servers    []mcp.ServerStatus `json:"servers"`
	GlobalPath string             `json:"globalPath"`
	FolderPath string             `json:"folderPath,omitempty"`
	// HasFolderConfig and Trusted describe the folder's mcp.json.
	HasFolderConfig bool              `json:"hasFolderConfig"`
	Trusted         bool              `json:"trusted"`
	Errors          map[string]string `json:"errors,omitempty"`
}

type mcpSink struct{ fn func(mcp.ServerStatus) }

func newMCPManager(dataDir string, sink *atomic.Pointer[mcpSink]) *mcp.Manager {
	return mcp.NewManager(dataDir, func(s mcp.ServerStatus) {
		if p := sink.Load(); p != nil && p.fn != nil {
			p.fn(s)
		}
	})
}

// SetMCPSink receives every MCP server status change.
func (r *AgentRuntime) SetMCPSink(fn func(mcp.ServerStatus)) { r.mcpSink.Store(&mcpSink{fn: fn}) }

// StartMCP starts the enabled global servers in the background so the
// first turn doesn't wait for them.
func (r *AgentRuntime) StartMCP() { go r.mcp.StartGlobal() }

// mcpTools are the MCP tools a turn in project sees; starting servers
// are waited for until ctx ends.
func (r *AgentRuntime) mcpTools(ctx context.Context, project string) []tools.Tool {
	return r.mcp.Tools(ctx, project)
}

func mcpSection(m *mcp.Manager, project string) string {
	text := m.Instructions(project)
	if strings.TrimSpace(text) == "" {
		return ""
	}
	return "## MCP servers\nTools named mcp__<server>__<tool> come from these servers. Their notes:\n\n" + text
}

// MCPList reports the servers visible from cwd.
func (r *AgentRuntime) MCPList(cwd string) MCPOverview {
	ov := MCPOverview{
		Servers:    r.mcp.Status(cwd),
		GlobalPath: mcp.GlobalPath(r.dataDir),
		Trusted:    true,
		Errors:     r.mcp.ConfigErrors(cwd),
	}
	if ov.Servers == nil {
		ov.Servers = []mcp.ServerStatus{}
	}
	if strings.TrimSpace(cwd) != "" {
		ov.FolderPath = mcp.FolderPath(cwd)
		cfg, err := r.mcp.Config(mcp.ScopeFolder, cwd)
		ov.HasFolderConfig = err == nil && cfg.Exists
		ov.Trusted = r.mcp.Trusted(cwd)
	}
	return ov
}

// MCPConfig returns one mcp.json for editing.
func (r *AgentRuntime) MCPConfig(scope, cwd string) (mcp.ConfigFile, error) {
	return r.mcp.Config(scope, cwd)
}

// MCPSetConfig validates, saves and applies one mcp.json.
func (r *AgentRuntime) MCPSetConfig(scope, cwd, text string) (MCPOverview, error) {
	if err := r.mcp.SetConfig(scope, cwd, text); err != nil {
		return MCPOverview{}, err
	}
	return r.MCPList(cwd), nil
}

// MCPSetEnabled turns one server on or off in its config file.
func (r *AgentRuntime) MCPSetEnabled(scope, cwd, name string, enabled bool) (MCPOverview, error) {
	if err := r.mcp.SetEnabled(scope, cwd, name, enabled); err != nil {
		return MCPOverview{}, err
	}
	return r.MCPList(cwd), nil
}

// MCPAdd adds parsed servers to one config file and starts them.
func (r *AgentRuntime) MCPAdd(scope, cwd string, servers []mcp.NamedServer, replace bool) (MCPOverview, error) {
	if err := r.mcp.AddServers(scope, cwd, servers, replace); err != nil {
		return MCPOverview{}, err
	}
	return r.MCPList(cwd), nil
}

// MCPRestart restarts one server, clearing any failure.
func (r *AgentRuntime) MCPRestart(scope, cwd, name string) (MCPOverview, error) {
	if err := r.mcp.Restart(scope, cwd, name); err != nil {
		return MCPOverview{}, err
	}
	return r.MCPList(cwd), nil
}

// MCPTrust approves or revokes the folder's mcp.json.
func (r *AgentRuntime) MCPTrust(cwd string, trust bool) (MCPOverview, error) {
	if err := r.mcp.Trust(cwd, trust); err != nil {
		return MCPOverview{}, err
	}
	return r.MCPList(cwd), nil
}
