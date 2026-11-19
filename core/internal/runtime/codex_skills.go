package runtime

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/Ady-JK/paladin/core/internal/codex"
)

const codexSkillsTimeout = 30 * time.Second

// CodexSkillView is a skill Codex offers in a folder.
type CodexSkillView struct {
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	Scope       string `json:"scope,omitempty"`
}

// CodexSkills lists the enabled skills Codex has for cwd, for the composer's
// $ menu.
func (r *AgentRuntime) CodexSkills(cwd string) ([]CodexSkillView, error) {
	ctx, cancel := context.WithTimeout(context.Background(), codexSkillsTimeout)
	defer cancel()
	client, err := r.codex.Client(ctx)
	if err != nil {
		return nil, err
	}
	skills, err := client.ListSkills(ctx, cwd)
	if err != nil {
		return nil, err
	}
	out := make([]CodexSkillView, 0, len(skills))
	for _, s := range skills {
		if s.Enabled {
			out = append(out, CodexSkillView{Name: s.Name, Title: s.Title, Description: s.Description, Scope: s.Scope})
		}
	}
	return out, nil
}

// skillToken matches a $name that stands alone: after the start, a space or an
// opening bracket, so a price or a variable like a$b is not one.
var skillToken = regexp.MustCompile(`(?:^|[\s(])\$([A-Za-z][\w:.-]*)`)

// skillMentions lists the distinct skill names a message invokes, in order.
func skillMentions(message string) []string {
	var names []string
	seen := map[string]bool{}
	for _, m := range skillToken.FindAllStringSubmatch(message, -1) {
		name := strings.TrimRight(m[1], ".:-")
		if name != "" && !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	return names
}

// mentionedSkills finds the skills a message names among the folder's. A
// message with no $name asks Codex nothing.
func mentionedSkills(ctx context.Context, client *codex.Client, cwd, message string) []codex.Skill {
	names := skillMentions(message)
	if len(names) == 0 {
		return nil
	}
	skills, err := client.ListSkills(ctx, cwd)
	if err != nil {
		return nil // the $name still goes to Codex as text, which it understands itself
	}
	var out []codex.Skill
	for _, name := range names {
		for _, s := range skills {
			if s.Enabled && s.Name == name {
				out = append(out, s)
			}
		}
	}
	return out
}
