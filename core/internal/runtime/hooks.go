package runtime

import (
	"github.com/Ady-JK/paladin/core/internal/hooks"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

// HooksOverview is what the Hooks settings pane shows: the global file
// and the folder's Paladin, Codex and Claude files.
type HooksOverview struct {
	Sources []hooks.Source `json:"sources"`
	Events  []hooks.Event  `json:"events"`
}

func (r *AgentRuntime) HooksList(cwd string) HooksOverview {
	return HooksOverview{Sources: r.hooks.List(cwd), Events: hooks.Events}
}

// HooksSetConfig writes the global or folder hooks.json (blank deletes).
func (r *AgentRuntime) HooksSetConfig(scope, cwd, text string) (HooksOverview, error) {
	if err := r.hooks.SetConfig(scope, cwd, text); err != nil {
		return HooksOverview{}, err
	}
	return r.HooksList(cwd), nil
}

// HooksTrust approves or revokes one of the folder's hooks files.
func (r *AgentRuntime) HooksTrust(cwd, path string, trust bool) (HooksOverview, error) {
	if err := r.hooks.Trust(cwd, path, trust); err != nil {
		return HooksOverview{}, err
	}
	return r.HooksList(cwd), nil
}

// HooksToggle turns one handler on or off.
func (r *AgentRuntime) HooksToggle(cwd, key string, enabled bool) (HooksOverview, error) {
	if err := r.hooks.Toggle(key, enabled); err != nil {
		return HooksOverview{}, err
	}
	return r.HooksList(cwd), nil
}

// sessionStartSource says whether this turn opens sess in this process:
// "startup" for a brand-new conversation, "resume" for one reopened
// after a restart, "" when SessionStart already ran (or has no hooks).
func (r *AgentRuntime) sessionStartSource(engine *hooks.Engine, sess transcript.Session) string {
	if !engine.Has(hooks.SessionStart) {
		return ""
	}
	r.startedMu.Lock()
	defer r.startedMu.Unlock()
	id := sess.Meta.SessionID
	if r.started[id] {
		return ""
	}
	r.started[id] = true
	if h, err := sess.History(); err == nil && len(h) > 0 {
		return "resume"
	}
	return "startup"
}
