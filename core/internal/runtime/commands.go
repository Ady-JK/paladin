// Skills and prompt commands: discovery for the UI, loading at turn time.

package runtime

import (
	"errors"
	"strconv"
	"strings"

	"github.com/Ady-JK/paladin/core/internal/agent"
	"github.com/Ady-JK/paladin/core/internal/commands"
	"github.com/Ady-JK/paladin/core/internal/skills"
)

// runtimeSkills adapts skill loading to agent.SkillSource: the global
// config dir plus the session's workspace.
type runtimeSkills struct{ r *AgentRuntime }

func (s runtimeSkills) LoadSkill(cwd, name string) (agent.Skill, error) {
	return s.LoadSkillForSession(cwd, "", name)
}

func (s runtimeSkills) LoadSkillForSession(cwd, sessionID, name string) (agent.Skill, error) {
	loaded, err := skills.Load(s.r.dataDir, cwd, name)
	if err != nil {
		return agent.Skill{}, err
	}
	return agent.Skill{Name: loaded.Name, Content: loaded.Content + s.r.skillSupplement(cwd, sessionID, loaded.Name)}, nil
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

func (c runtimeCommands) LoadCommand(cwd, sessionID, name, args string) (agent.Command, error) {
	cmd, err := commands.Load(c.r.dataDir, cwd, strings.TrimPrefix(name, "/"))
	if err != nil {
		return agent.Command{}, err
	}
	// The diff goes in after expansion so a "$1" inside it stays as is.
	prompt := commands.Expand(cmd.Body, args)
	if strings.EqualFold(cmd.Name, "create-agent") {
		guide, err := skills.Load(c.r.dataDir, cwd, "create-agent")
		if err != nil {
			return agent.Command{}, err
		}
		prompt += "\n\n" + guide.Content + c.r.agentCapabilityContext(cwd, sessionID)
	}
	if strings.Contains(cmd.Body, sessionChangesSlot) {
		changes, err := c.r.sessionChangesFor(sessionID, cwd, args)
		if err != nil {
			return agent.Command{}, err
		}
		prompt = strings.Replace(prompt, sessionChangesSlot, changes, 1)
	}
	return agent.Command{Name: cmd.Name, Prompt: prompt, Mode: cmd.Mode, Tools: cmd.Tools}, nil
}

// sessionChangesSlot in a command body becomes the session's own file
// changes as a unified diff when the command's argument is "session"
// (as in `/review session`), and nothing otherwise. When the session
// has no snapshots or changed nothing, the command fails before the
// model runs: substituting other changes would review work that may not
// be this session's.
const sessionChangesSlot = "$SESSION_CHANGES"

// maxSessionChanges caps the diff a command prompt can carry.
const maxSessionChanges = 100 * 1024

func (r *AgentRuntime) sessionChangesFor(sessionID, cwd, args string) (string, error) {
	if !strings.EqualFold(strings.TrimSpace(args), "session") {
		return "", nil
	}
	const other = " Run /review for the uncommitted changes, or /review <branch or commit>"
	view, err := r.SessionChanges(sessionID, cwd)
	if err != nil || !view.Available {
		return "", errors.New("/review session: session changes unavailable, since this session has no snapshots to tell its own changes apart." + other)
	}
	if len(view.Files) == 0 {
		return "", errors.New("/review session: this session has not changed any files, so there is nothing to review." + other)
	}
	return renderSessionChanges(view.Files, func(path string) (string, error) {
		d, err := r.SessionDiff(sessionID, cwd, -1, path)
		return d.Patch, err
	})
}

// renderSessionChanges is the diff of files within maxSessionChanges. A file
// whose diff fails or does not fit is named with the reason, so the
// review never takes a partial diff for the whole; when no diff can be
// built at all, it is an error.
func renderSessionChanges(files []ChangedFile, diff func(path string) (string, error)) (string, error) {
	var body strings.Builder
	var left []string
	full := false
	for _, f := range files {
		if full {
			left = append(left, f.Path+": over the "+strconv.Itoa(maxSessionChanges>>10)+" KB limit")
			continue
		}
		patch, err := diff(f.Path)
		if err != nil {
			left = append(left, f.Path+": could not build its diff ("+err.Error()+")")
			continue
		}
		if body.Len()+len(patch) > maxSessionChanges {
			full = true
			left = append(left, f.Path+": over the "+strconv.Itoa(maxSessionChanges>>10)+" KB limit")
			continue
		}
		body.WriteString(strings.TrimRight(patch, "\n") + "\n")
	}
	if body.Len() == 0 {
		return "", errors.New("/review session: could not build a diff for any changed file (" + strings.Join(left, "; ") + ")")
	}
	var b strings.Builder
	if len(left) == 0 {
		b.WriteString("Session changes (unified diff of every file this session changed, from Paladin's snapshots; paths are relative to the workspace):\n")
	} else {
		b.WriteString("Session changes (unified diff of the files this session changed, from Paladin's snapshots; paths are relative to the workspace; " +
			"some files are not included, see the list after the diff):\n")
	}
	b.WriteString("```diff\n" + body.String() + "```")
	if len(left) > 0 {
		b.WriteString("\nNot included (read these files directly):\n- " + strings.Join(left, "\n- "))
	}
	return b.String(), nil
}

// ListCommands returns the prompt commands for a workspace.
func (r *AgentRuntime) ListCommands(cwd string) []commands.Command {
	list := commands.List(r.dataDir, resolveCwd(r, cwd))
	if list == nil {
		list = []commands.Command{}
	}
	return list
}
