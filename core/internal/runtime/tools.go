package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/Ady-JK/paladin/core/internal/jsonx"
	"github.com/Ady-JK/paladin/core/internal/mcp"
	"github.com/Ady-JK/paladin/core/internal/tools"
	"github.com/Ady-JK/paladin/core/internal/toolset"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

// Tool switch scopes.
const (
	ToolScopeGlobal  = "global"
	ToolScopeSession = "session"
)

// ToolInfo is one tool as the tool switches UI shows it.
type ToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Source is "builtin" or "mcp"; MCP tools also name their server
	// and the server's own name for the tool.
	Source      string `json:"source"`
	Server      string `json:"server,omitempty"`
	Tool        string `json:"tool,omitempty"`
	ReadOnly    bool   `json:"readOnly,omitempty"`
	Destructive bool   `json:"destructive,omitempty"`
	// Global and Session are the switches set at each scope; nil means
	// unset. Enabled is the result.
	Global  *bool `json:"global,omitempty"`
	Session *bool `json:"session,omitempty"`
	Enabled bool  `json:"enabled"`
	// Pending is a new tool this session holds back until accepted.
	Pending bool `json:"pending,omitempty"`
}

// ToolsOverview lists every tool a folder (and session) can see.
type ToolsOverview struct {
	Tools   []ToolInfo `json:"tools"`
	Pending []string   `json:"pending"`
}

// ToolsEvent is the tools/pending event: a turn started without the
// session's new tools because the user has not accepted them yet.
type ToolsEvent struct {
	Event     string   `json:"event"`
	SessionID string   `json:"sessionId"`
	Pending   []string `json:"pending"`
}

type toolsSink struct{ fn func(ToolsEvent) }

// SetToolsSink receives every tools/pending event.
func (r *AgentRuntime) SetToolsSink(fn func(ToolsEvent)) { r.toolsSink.Store(&toolsSink{fn: fn}) }

func (r *AgentRuntime) emitTools(e ToolsEvent) {
	if p := r.toolsSink.Load(); p != nil && p.fn != nil {
		p.fn(e)
	}
}

type toolsFile struct {
	Tools map[string]bool `json:"tools,omitempty"`
}

func (r *AgentRuntime) toolsPath() string { return filepath.Join(r.dataDir, "config", "tools.json") }

// globalToolSwitches reads the global switches; a missing or broken
// file means every tool is on.
func (r *AgentRuntime) globalToolSwitches() map[string]bool {
	data, err := os.ReadFile(r.toolsPath())
	if err != nil {
		return nil
	}
	var f toolsFile
	if json.Unmarshal(data, &f) != nil {
		return nil
	}
	return f.Tools
}

func (r *AgentRuntime) setGlobalToolSwitches(names []string, value *bool) error {
	r.toolsMu.Lock()
	defer r.toolsMu.Unlock()
	f := toolsFile{Tools: toolset.SetOverride(r.globalToolSwitches(), names, value)}
	data, err := jsonx.MarshalIndent(f)
	if err != nil {
		return err
	}
	return mcp.WriteFile(r.toolsPath(), data)
}

// sessionTools loads a session's tool state and the folder its MCP
// servers belong to. An empty sessionID is not an error.
func (r *AgentRuntime) sessionTools(cwd, sessionID string) (*toolset.State, string, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, cwd, nil
	}
	sess, err := r.transcripts.Load(cwd, sessionID)
	if err != nil {
		return nil, "", err
	}
	return sess.Meta.Tools, sess.Meta.ProjectCwd(), nil
}

// ToolsList reports every tool visible from cwd, with its switches and,
// for a session, whether it is pending. MCP tools come from servers
// that are running now.
func (r *AgentRuntime) ToolsList(cwd, sessionID string) (ToolsOverview, error) {
	st, project, err := r.sessionTools(cwd, sessionID)
	if err != nil {
		return ToolsOverview{}, err
	}
	var list []ToolInfo
	for _, name := range r.tools.Names() {
		t, _ := r.tools.Get(name)
		list = append(list, ToolInfo{Name: name, Description: t.Definition().Description, Source: "builtin"})
	}
	for _, srv := range r.mcp.Status(project) {
		if srv.Shadowed || srv.State != mcp.StateReady {
			continue
		}
		for _, t := range srv.Tools {
			list = append(list, ToolInfo{
				Name: t.Exposed, Description: t.Description, Source: "mcp",
				Server: srv.Name, Tool: t.Name, ReadOnly: t.ReadOnly, Destructive: t.Destructive,
			})
		}
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].Source != list[j].Source {
			return list[i].Source == "builtin"
		}
		return list[i].Name < list[j].Name
	})

	global := r.globalToolSwitches()
	var session map[string]bool
	if st != nil {
		session = st.Overrides
	}
	names := make([]string, len(list))
	for i := range list {
		names[i] = list[i].Name
	}
	res := toolset.Resolve(names, global, st)
	for i := range list {
		ti := &list[i]
		if v, ok := global[ti.Name]; ok {
			ti.Global = &v
		}
		if v, ok := session[ti.Name]; ok {
			ti.Session = &v
		}
		ti.Enabled = toolset.Enabled(ti.Name, global, session)
		ti.Pending = slices.Contains(res.Pending, ti.Name)
	}
	if list == nil {
		list = []ToolInfo{}
	}
	pending := res.Pending
	if pending == nil {
		pending = []string{}
	}
	return ToolsOverview{Tools: list, Pending: pending}, nil
}

// ToolsSet switches tools on (true), off (false) or back to the
// inherited value (nil) globally or for one session.
func (r *AgentRuntime) ToolsSet(scope, cwd, sessionID string, names []string, value *bool) (ToolsOverview, error) {
	names = cleanNames(names)
	if len(names) == 0 {
		return ToolsOverview{}, fmt.Errorf("no tool names given")
	}
	switch scope {
	case ToolScopeGlobal:
		if err := r.setGlobalToolSwitches(names, value); err != nil {
			return ToolsOverview{}, err
		}
	case ToolScopeSession:
		if strings.TrimSpace(sessionID) == "" {
			return ToolsOverview{}, fmt.Errorf("a session switch needs a sessionId")
		}
		err := r.transcripts.UpdateMeta(cwd, sessionID, func(m *transcript.Meta) error {
			st := m.Tools
			if st == nil {
				st = &toolset.State{}
			}
			st.Overrides = toolset.SetOverride(st.Overrides, names, value)
			m.Tools = st
			return nil
		})
		if err != nil {
			return ToolsOverview{}, err
		}
	default:
		return ToolsOverview{}, fmt.Errorf("scope must be %q or %q, got %q", ToolScopeGlobal, ToolScopeSession, scope)
	}
	return r.ToolsList(cwd, sessionID)
}

// ToolsAccept lets a session use its pending tools (all of them when
// names is empty) from its next turn.
func (r *AgentRuntime) ToolsAccept(cwd, sessionID string, names []string) (ToolsOverview, error) {
	if strings.TrimSpace(sessionID) == "" {
		return ToolsOverview{}, fmt.Errorf("sessionId is required")
	}
	names = cleanNames(names)
	if len(names) == 0 {
		ov, err := r.ToolsList(cwd, sessionID)
		if err != nil {
			return ToolsOverview{}, err
		}
		names = ov.Pending
	}
	err := r.transcripts.UpdateMeta(cwd, sessionID, func(m *transcript.Meta) error {
		if m.Tools == nil {
			m.Tools = &toolset.State{}
		}
		m.Tools.Accept(names)
		return nil
	})
	if err != nil {
		return ToolsOverview{}, err
	}
	return r.ToolsList(cwd, sessionID)
}

var errMetaUnchanged = errors.New("meta unchanged")

// turnTools narrows a turn's registry to the switched-on, accepted
// tools, records what the session has now seen and reports held-back
// tools as tools/pending.
func (r *AgentRuntime) turnTools(cwd, sessionID string, all *tools.Registry) *tools.Registry {
	global := r.globalToolSwitches()
	var res toolset.Resolution
	err := r.transcripts.UpdateMeta(cwd, sessionID, func(m *transcript.Meta) error {
		res = toolset.Resolve(all.Names(), global, m.Tools)
		st := m.Tools
		if st == nil {
			st = &toolset.State{}
		}
		if st.Seeded && slices.Equal(st.Known, res.Known) {
			return errMetaUnchanged
		}
		st.Known, st.Seeded = res.Known, true
		m.Tools = st
		return nil
	})
	if err != nil && !errors.Is(err, errMetaUnchanged) {
		res = toolset.Resolve(all.Names(), global, nil)
	}
	if len(res.Pending) > 0 {
		r.emitTools(ToolsEvent{Event: "tools/pending", SessionID: sessionID, Pending: res.Pending})
	}
	return all.Filter(func(name string) bool { return slices.Contains(res.Enabled, name) })
}

// switchedOffSection tells the model which built-in tools the user
// turned off, since the tool guidance still describes them.
func (r *AgentRuntime) switchedOffSection(meta transcript.Meta) string {
	global := r.globalToolSwitches()
	var session map[string]bool
	if meta.Tools != nil {
		session = meta.Tools.Overrides
	}
	var off []string
	for _, name := range r.tools.Names() {
		if !toolset.Enabled(name, global, session) {
			off = append(off, name)
		}
	}
	if len(off) == 0 {
		return ""
	}
	return "## Tools switched off\nThe user switched off these tools for this session: " + strings.Join(off, ", ") +
		". They are not available; ignore any guidance about them and work with the tools you have."
}

func cleanNames(names []string) []string {
	var out []string
	for _, n := range names {
		if n = strings.TrimSpace(n); n != "" && !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out
}
