package runtime

// PermissionsView is the settings pane snapshot. Agents run with full
// access and there are no saved rules, so only the preset is reported; the
// empty lists keep the older wire shape readable.
type PermissionsView struct {
	Global  []struct{} `json:"global"`
	Folder  []struct{} `json:"folder"`
	Session []struct{} `json:"session"`
	Folders []struct{} `json:"folders"`
	Presets []string   `json:"presets"`
}

// PermissionsGet reports the permission view: full access only.
func (r *AgentRuntime) PermissionsGet(string, ...string) (PermissionsView, error) {
	return PermissionsView{Global: []struct{}{}, Folder: []struct{}{}, Session: []struct{}{}, Folders: []struct{}{},
		Presets: []string{"full-access"}}, nil
}

// PermissionsSet is kept for older clients: there are no rules to save.
func (r *AgentRuntime) PermissionsSet(scope, cwd string) (PermissionsView, error) {
	return r.PermissionsGet(cwd)
}
