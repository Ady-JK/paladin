package mcp

import (
	"strings"
	"time"
)

// Transport kinds.
const (
	TransportStdio = "stdio"
	TransportHTTP  = "http"
)

// Defaults for servers that set no timeouts.
const (
	DefaultStartupTimeout = 30 * time.Second
	DefaultToolTimeout    = 120 * time.Second
)

// ServerConfig is one entry of an mcpServers map. The shape follows the
// common mcp.json convention (Claude, Cursor, VS Code), so existing
// configs paste in unchanged.
type ServerConfig struct {
	// Type is "stdio" or "http"; empty infers it from command / url.
	Type    string            `json:"type,omitempty"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Cwd     string            `json:"cwd,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	// Enabled defaults to true; disabled servers are listed but never
	// started.
	Enabled           *bool    `json:"enabled,omitempty"`
	StartupTimeoutSec float64  `json:"startupTimeoutSec,omitempty"`
	ToolTimeoutSec    float64  `json:"toolTimeoutSec,omitempty"`
	AllowedTools      []string `json:"allowedTools,omitempty"`
	DeniedTools       []string `json:"deniedTools,omitempty"`
}

// Transport resolves the transport kind; "" means the config is unusable.
func (c ServerConfig) Transport() string {
	switch strings.ToLower(strings.TrimSpace(c.Type)) {
	case "stdio":
		if strings.TrimSpace(c.Command) != "" {
			return TransportStdio
		}
		return ""
	case "http", "streamable-http", "streamablehttp":
		if strings.TrimSpace(c.URL) != "" {
			return TransportHTTP
		}
		return ""
	case "":
		switch {
		case strings.TrimSpace(c.Command) != "":
			return TransportStdio
		case strings.TrimSpace(c.URL) != "":
			return TransportHTTP
		}
	}
	return ""
}

// IsEnabled reports whether the server should be started.
func (c ServerConfig) IsEnabled() bool { return c.Enabled == nil || *c.Enabled }

// StartupTimeout bounds launch plus initialize plus the first listing.
func (c ServerConfig) StartupTimeout() time.Duration {
	if c.StartupTimeoutSec > 0 {
		return time.Duration(c.StartupTimeoutSec * float64(time.Second))
	}
	return DefaultStartupTimeout
}

// ToolTimeout bounds one tools/call.
func (c ServerConfig) ToolTimeout() time.Duration {
	if c.ToolTimeoutSec > 0 {
		return time.Duration(c.ToolTimeoutSec * float64(time.Second))
	}
	return DefaultToolTimeout
}
