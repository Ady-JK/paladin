package codex

import (
	"context"
	"slices"
	"time"
)

// Permission presets, as the UI names them.
const (
	PresetReadOnly   = "read-only"
	PresetAuto       = "auto"
	PresetFullAccess = "full-access"
)

// Requirements are the limits an administrator (or the user's own
// requirements file) puts on Codex: only these sandbox modes and approval
// policies are accepted, and Codex rejects anything else outright. A nil list
// means "no limit".
type Requirements struct {
	Sandboxes []string `json:"allowedSandboxModes"`
	Approvals []string `json:"allowedApprovalPolicies"`
}

func allows(list []string, value string) bool { return list == nil || slices.Contains(list, value) }

// Requirements reads the limits once per process; a Codex that cannot say
// (an older version) is treated as unrestricted.
func (c *Client) Requirements() Requirements {
	c.reqOnce.Do(func() {
		// Its own deadline: the answer is kept for every later turn, so a
		// turn that is stopped meanwhile must not cancel the read.
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		var res struct {
			Requirements *Requirements `json:"requirements"`
		}
		if err := c.Call(ctx, "configRequirements/read", map[string]any{}, &res); err == nil && res.Requirements != nil {
			c.requirements = *res.Requirements
		}
	})
	return c.requirements
}

// Choice is the Codex settings a permission preset maps to.
type Choice struct {
	Preset   string
	Approval string
	Sandbox  string // thread setting: read-only, workspace-write, danger-full-access
}

// SandboxPolicy is the same sandbox in the form turns take.
func (c Choice) SandboxPolicy() map[string]any {
	switch c.Sandbox {
	case "read-only":
		return map[string]any{"type": "readOnly"}
	case "danger-full-access":
		return map[string]any{"type": "dangerFullAccess"}
	}
	return map[string]any{"type": "workspaceWrite"}
}

// exact maps one preset to Codex settings the requirements accept.
func (r Requirements) exact(preset string) (Choice, bool) {
	var sandbox string
	var approvals []string
	switch preset {
	case PresetReadOnly:
		sandbox, approvals = "read-only", []string{"on-request", "untrusted"}
	case PresetAuto:
		sandbox, approvals = "workspace-write", []string{"on-request", "untrusted"}
	case PresetFullAccess:
		sandbox, approvals = "danger-full-access", []string{"never"}
	default:
		return Choice{}, false
	}
	if !allows(r.Sandboxes, sandbox) {
		return Choice{}, false
	}
	for _, a := range approvals {
		if allows(r.Approvals, a) {
			return Choice{Preset: preset, Approval: a, Sandbox: sandbox}, true
		}
	}
	return Choice{}, false
}

// Choose maps a preset to settings Codex accepts. A preset the requirements
// forbid falls back to the next safer one (full access to auto to read-only),
// never to a looser one; ok is false when even read-only is forbidden.
func (r Requirements) Choose(preset string) (Choice, bool) {
	order := []string{PresetReadOnly}
	switch preset {
	case PresetFullAccess:
		order = []string{PresetFullAccess, PresetAuto, PresetReadOnly}
	case PresetAuto, "":
		order = []string{PresetAuto, PresetReadOnly}
	}
	for _, p := range order {
		if c, ok := r.exact(p); ok {
			return c, true
		}
	}
	return Choice{}, false
}

// Presets lists the presets the requirements allow as they are.
func (r Requirements) Presets() []string {
	out := []string{}
	for _, p := range []string{PresetReadOnly, PresetFullAccess} {
		if _, ok := r.exact(p); ok {
			out = append(out, p)
		}
	}
	return out
}
