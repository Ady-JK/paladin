// Package permission is Paladin's safety net for native tool calls. Agents
// run with full freedom; the guard (guard.go) asks the user once before a
// catastrophic or security-damaging action, and the Gate (gate.go) wires it
// into the tool loop. There are no modes and no saved rules, and execution
// is not OS-sandboxed.
package permission

import (
	"fmt"
	"strings"
)

// Preset is the session permission level. Only full access exists; the
// older "read-only" and "auto" values are accepted and mean the same.
type Preset string

// FullAccess is the one preset.
const FullAccess Preset = "full-access"

// ParsePreset maps the permissions a request carries. Anything the older
// versions sent is accepted and ignored.
func ParsePreset(s string) (Preset, error) {
	switch strings.TrimSpace(s) {
	case "", "auto", "read-only", string(FullAccess):
		return FullAccess, nil
	}
	return "", fmt.Errorf("permissions must be %q, got %q", FullAccess, s)
}

// Request kinds, shown on approval cards.
const (
	KindShell       = "shell"
	KindWrite       = "write"
	KindRead        = "read"
	KindMCP         = "mcp"
	KindPermissions = "permissions"
	// KindProject is a card a ProjectTrust gate may raise for a tool the
	// workspace ships; Paladin's own gate never does.
	KindProject = "project"
)

// Replies a user can give on an approval card. Guard cards offer only
// once and deny; the others are for Codex's own approvals.
const (
	ReplyOnce    = "once"
	ReplySession = "session"
	ReplyFolder  = "folder"
	ReplyGlobal  = "global"
	ReplyDeny    = "deny"
)
