package runtime

import (
	"strings"

	"github.com/Ady-JK/paladin/core/internal/permission"
	"github.com/Ady-JK/paladin/core/internal/sandbox"
)

// PermissionsView is the settings pane's snapshot: saved rules, the
// OS sandbox in use ("" means approvals only) and the presets.
type PermissionsView struct {
	Global  []permission.Rule        `json:"global"`
	Folder  []permission.Rule        `json:"folder"`
	Session []permission.Rule        `json:"session"`
	Folders []permission.FolderRules `json:"folders"`
	Sandbox SandboxInfo              `json:"sandbox"`
	Presets []string                 `json:"presets"`
}

type SandboxInfo struct {
	Kind string `json:"kind"`
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// PermissionsGet returns the rules that apply to cwd (and to a session
// when one is given), plus every folder that has saved rules.
func (r *AgentRuntime) PermissionsGet(cwd string, sessionID ...string) (PermissionsView, error) {
	cwd = resolveCwd(r, cwd)
	global, err := r.perms.Rules(permission.ScopeGlobal, "", "")
	if err != nil {
		return PermissionsView{}, err
	}
	var folder []permission.Rule
	if strings.TrimSpace(cwd) != "" {
		if folder, err = r.perms.Rules(permission.ScopeFolder, cwd, ""); err != nil {
			return PermissionsView{}, err
		}
	}
	var sess []permission.Rule
	if len(sessionID) > 0 && strings.TrimSpace(sessionID[0]) != "" {
		sess, _ = r.perms.Rules(permission.ScopeSession, "", sessionID[0])
	}
	return PermissionsView{
		Global:  nonNil(global),
		Folder:  nonNil(folder),
		Session: nonNil(sess),
		Folders: nonNil(r.perms.Folders()),
		Sandbox: SandboxInfo{Kind: sandbox.Kind()},
		Presets: []string{string(permission.ReadOnly), string(permission.Auto), string(permission.FullAccess)},
	}, nil
}

// PermissionsSet replaces the global or one folder's saved rules.
func (r *AgentRuntime) PermissionsSet(scope, cwd string, rules []permission.Rule) (PermissionsView, error) {
	if scope == permission.ScopeFolder {
		cwd = resolveCwd(r, cwd)
	}
	if err := r.perms.Set(scope, cwd, rules); err != nil {
		return PermissionsView{}, err
	}
	return r.PermissionsGet(cwd)
}
