// Knowledge features wired into turns — project instructions
// (AGENTS.md), the skill catalog and Hermes-style memory — plus the
// memory management calls the UI makes.

package runtime

import (
	"fmt"
	"strings"

	"github.com/Ady-JK/paladin/core/internal/instructions"
	"github.com/Ady-JK/paladin/core/internal/memory"
	"github.com/Ady-JK/paladin/core/internal/tools"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

// skillCatalogMax caps the skills listed in the system prompt.
const skillCatalogMax = 40

// registerKnowledgeTools adds the tools that need the data dir or the
// transcript store to the shared registry.
func (r *AgentRuntime) registerKnowledgeTools() {
	r.memory = memory.NewBankWithConfig(r.dataDir, memory.LoadConfigOr(r.dataDir))
	r.tools.Register(memory.NewTool(r.memory))
	r.tools.Register(tools.NewSkillTool(r.dataDir))
	r.tools.Register(tools.NewSessionSearchTool(r.transcripts))
}

// runtimeSections adapts the knowledge sources to agent.SectionSource.
// Order matters for prompt caching: the slow-changing instructions and
// skill list come first, the per-session frozen memory snapshot last.
type runtimeSections struct{ r *AgentRuntime }

func (s runtimeSections) SystemSections(sess transcript.Session) []string {
	cwd := sess.Meta.Cwd
	out := []string{instructions.Render(instructions.Discover(s.r.dataDir, cwd))}
	if _, ok := s.r.tools.Get("skill"); ok {
		out = append(out, tools.SkillCatalog(s.r.dataDir, cwd, skillCatalogMax))
	}
	out = append(out, mcpSection(s.r.mcp, sess.Meta.ProjectCwd()))
	if s.r.memory != nil {
		out = append(out, s.r.memory.SessionSnapshot(sess.Dir, sess.Meta.ProjectCwd()))
	}
	if wt := sess.Meta.Worktree; wt != nil {
		out = append(out, worktreeSection(wt))
	}
	out = append(out, s.r.switchedOffSection(sess.Meta))
	return out
}

// MemoryView is one memory store as the settings pane shows it.
type MemoryView struct {
	Target  string   `json:"target"`
	Cwd     string   `json:"cwd,omitempty"`
	Path    string   `json:"path"`
	Entries []string `json:"entries"`
	Used    int      `json:"used"`
	Limit   int      `json:"limit"`
	// Enabled is false when memory.json switches this scope off: the
	// pane still shows the stored entries but refuses new ones.
	Enabled bool `json:"enabled"`
}

func (r *AgentRuntime) memoryStore(target, cwd string) (memory.Target, string, error) {
	t, err := memory.ParseTarget(target)
	if err != nil {
		return "", "", err
	}
	if t == memory.TargetProject {
		cwd = strings.TrimSpace(cwd)
		if cwd == "" {
			return "", "", fmt.Errorf("invalid: cwd is required for project memory")
		}
	}
	return t, cwd, nil
}

// MemoryGet reads one memory store.
func (r *AgentRuntime) MemoryGet(target, cwd string) (MemoryView, error) {
	t, cwd, err := r.memoryStore(target, cwd)
	if err != nil {
		return MemoryView{}, err
	}
	res, err := r.memory.Read(t, cwd)
	if err != nil {
		return MemoryView{}, err
	}
	return r.memoryView(t, cwd, res)
}

// MemorySet replaces one memory store's entries (the settings editor).
// The same limits and threat scan as the model's tool apply.
func (r *AgentRuntime) MemorySet(target, cwd string, entries []string) (MemoryView, error) {
	t, cwd, err := r.memoryStore(target, cwd)
	if err != nil {
		return MemoryView{}, err
	}
	res, err := r.memory.ReplaceAll(t, cwd, entries)
	if err != nil {
		return MemoryView{}, err
	}
	return r.memoryView(t, cwd, res)
}

func (r *AgentRuntime) memoryView(t memory.Target, cwd string, res memory.Result) (MemoryView, error) {
	st, err := r.memory.Store(t, cwd)
	if err != nil {
		return MemoryView{}, err
	}
	entries := res.Entries
	if entries == nil {
		entries = []string{}
	}
	v := MemoryView{Target: string(t), Path: st.Path(), Entries: entries, Used: res.Used, Limit: res.Limit, Enabled: r.memory.Enabled(t)}
	if t == memory.TargetProject {
		v.Cwd = cwd
	}
	return v, nil
}

// MemoryProjects lists folders with memory.
func (r *AgentRuntime) MemoryProjects() []memory.Project {
	list := r.memory.Projects()
	if list == nil {
		list = []memory.Project{}
	}
	return list
}

// MemoryConfigView is memory.json as the RPC reports it, for the pane's
// budget labels and the switch state.
type MemoryConfigView struct {
	Limits      map[string]int `json:"limits"`
	Disabled    []string       `json:"disabled"`
	Provider    string         `json:"provider"`
	ReviewTurns int            `json:"reviewTurns"`
}

// MemoryConfig reports the effective memory settings.
func (r *AgentRuntime) MemoryConfig() MemoryConfigView {
	cfg := r.memory.Config()
	return MemoryConfigView{
		Limits: map[string]int{
			string(memory.TargetUser):    cfg.LimitFor(memory.TargetUser),
			string(memory.TargetGlobal):  cfg.LimitFor(memory.TargetGlobal),
			string(memory.TargetProject): cfg.LimitFor(memory.TargetProject),
		},
		Disabled:    append([]string{}, cfg.Disabled...),
		Provider:    cfg.Provider,
		ReviewTurns: cfg.ReviewTurns,
	}
}

// ReloadMemoryConfig re-reads memory.json after the settings editor saves
// it, so a limit or enable change reaches the running runtime.
func (r *AgentRuntime) ReloadMemoryConfig() error {
	cfg, err := memory.LoadConfig(r.dataDir)
	if err != nil {
		return err
	}
	r.memory.SetConfig(cfg)
	return nil
}

// MemorySnapshotStatus says whether the open session's frozen snapshot is
// behind the stores on disk. The pane uses it to offer the refresh RPC
// instead of leaving the user to guess.
func (r *AgentRuntime) MemorySnapshotStatus(sessionId, cwd string) (memory.SnapshotState, error) {
	sess, err := r.transcripts.Load(cwd, sessionId)
	if err != nil {
		return memory.SnapshotState{}, err
	}
	return r.memory.SnapshotStatus(sess.Dir, sess.Meta.ProjectCwd()), nil
}

// RefreshMemorySnapshot drops a session's frozen memory snapshot so its
// next turn sees the current memory.
func (r *AgentRuntime) RefreshMemorySnapshot(sessionID, cwd string) error {
	sess, err := r.transcripts.Load(cwd, sessionID)
	if err != nil {
		return err
	}
	return r.memory.ResetSessionSnapshot(sess.Dir)
}
