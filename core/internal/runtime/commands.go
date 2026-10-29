// Skills and prompt commands: discovery for the UI, loading at turn time.

package runtime

import (
	"strings"

	"github.com/Ady-JK/paladin/core/internal/agent"
	"github.com/Ady-JK/paladin/core/internal/commands"
	"github.com/Ady-JK/paladin/core/internal/skills"
)

// runtimeSkills adapts skill loading to agent.SkillSource: the global
// config dir plus the session's workspace.
type runtimeSkills struct{ r *AgentRuntime }

func (s runtimeSkills) LoadSkill(cwd, name string) (agent.Skill, error) {
	loaded, err := skills.Load(s.r.dataDir, cwd, name)
	if err != nil {
		return agent.Skill{}, err
	}
	return agent.Skill{Name: loaded.Name, Content: loaded.Content}, nil
}

// SkillEntry is one listed skill: name, description, source
// (global/workspace) and defining path. Content is only returned by
// LoadSkill, never by listing, so the picker stays light.
type SkillEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"`
	Path        string `json:"path"`
}

// ListSkills discovers skills from the global config dir and the
// workspace dir. A workspace skill shadows a global one of the same
// name.
func (r *AgentRuntime) ListSkills(cwd string) []SkillEntry {
	found := skills.List(r.dataDir, resolveCwd(r, cwd))
	out := make([]SkillEntry, 0, len(found))
	for _, s := range found {
		out = append(out, SkillEntry{Name: s.Name, Description: s.Description, Source: s.Source, Path: s.Path})
	}
	return out
}

// runtimeCommands adapts prompt commands to agent.CommandSource.
type runtimeCommands struct{ r *AgentRuntime }

func (c runtimeCommands) LoadCommand(cwd, name, args string) (agent.Command, error) {
	cmd, err := commands.Load(c.r.dataDir, cwd, strings.TrimPrefix(name, "/"))
	if err != nil {
		return agent.Command{}, err
	}
	return agent.Command{Name: cmd.Name, Prompt: commands.Expand(cmd.Body, args), Mode: cmd.Mode}, nil
}

// ListCommands returns the prompt commands for a workspace.
func (r *AgentRuntime) ListCommands(cwd string) []commands.Command {
	list := commands.List(r.dataDir, resolveCwd(r, cwd))
	if list == nil {
		list = []commands.Command{}
	}
	return list
}
