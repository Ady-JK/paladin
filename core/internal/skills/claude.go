package skills

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Claude Code and Agent Skills roots.
//
// Claude Code skills use the same directory + SKILL.md + frontmatter
// shape Paladin already reads, so nothing here translates content: the
// adapter is discovery. A skill installed for another assistant appears
// in Paladin's `/` palette and injects exactly the same file.
//
// Paladin's own skills always win over these. Among the external roots,
// a project skill wins over a user skill and a plugin skill loses to a
// standalone one of the same name.
const (
	SourceClaudeUser    = "claude"
	SourceClaudeProject = "claude-project"
	SourceClaudePlugin  = "claude-plugin"
	SourceAgentsUser    = "agents"
	SourceAgentsProject = "agents-project"

	// PluginSkillPrefix namespaces a skill contributed by an installed
	// Claude plugin, matching Claude Code's own `plugin:review` form.
	PluginSkillPrefix = "plugin:"
)

// root is one directory scanned for <name>/SKILL.md skill directories.
type root struct {
	dir    string
	source string
}

// claudeRoots lists the external skill roots visible from cwd, highest
// priority first: a project's own copy beats a user-wide one, and a
// standalone skill beats a plugin's. List and Load both stop at the
// first root that defines a name.
func claudeRoots(cwd string) []root {
	home := homeDir()
	var out []root
	add := func(dir, source string) {
		if strings.TrimSpace(dir) != "" {
			out = append(out, root{dir: dir, source: source})
		}
	}
	if strings.TrimSpace(cwd) != "" {
		add(filepath.Join(cwd, ".claude", "skills"), SourceClaudeProject)
		add(filepath.Join(cwd, ".agents", "skills"), SourceAgentsProject)
	}
	if home != "" {
		add(filepath.Join(home, ".claude", "skills"), SourceClaudeUser)
		// Plugins install under the user's Claude dir, so they rank with
		// user skills, below a project's own copy.
		for _, dir := range pluginSkillDirs(home) {
			out = append(out, root{dir: dir, source: SourceClaudePlugin})
		}
		add(filepath.Join(home, ".agents", "skills"), SourceAgentsUser)
	}
	return out
}

// pluginSkillDirs finds the skills directory of every installed Claude
// plugin. Plugin layouts differ (some nest under a marketplace), so the
// walk looks for directories named "skills" a few levels down rather
// than assuming one shape.
func pluginSkillDirs(home string) []string {
	base := filepath.Join(home, ".claude", "plugins")
	if _, err := os.Stat(base); err != nil {
		return nil
	}
	var out []string
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		if depth > 4 {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			child := filepath.Join(dir, e.Name())
			if e.Name() == "skills" && hasSkillDir(child) {
				out = append(out, child)
				continue
			}
			walk(child, depth+1)
		}
	}
	walk(base, 0)
	sort.Strings(out)
	return out
}

// hasSkillDir reports whether dir holds at least one <name>/SKILL.md.
func hasSkillDir(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, _, ok := readSkillFile(filepath.Join(dir, e.Name())); ok {
			return true
		}
	}
	return false
}

// scanPluginRoot lists the skills under one plugin skills directory,
// prefixing each name with plugin:<plugin>. The plugin name is the
// directory above "skills".
func scanPluginRoot(dir string) []Skill {
	skills := scanRoot(dir, SourceClaudePlugin)
	plugin := filepath.Base(filepath.Dir(dir))
	for i := range skills {
		skills[i].Name = PluginSkillPrefix + plugin + ":" + skills[i].Name
	}
	return skills
}

// splitPluginSkillName splits a `plugin:<plugin>:<skill>` lookup into
// its plugin and skill halves. A plugin name never contains a colon, so
// the first one after the prefix separates the two. The prefix matches
// case-insensitively; the halves come back as written.
func splitPluginSkillName(name string) (plugin, skill string, ok bool) {
	if len(name) <= len(PluginSkillPrefix) || !strings.EqualFold(name[:len(PluginSkillPrefix)], PluginSkillPrefix) {
		return "", "", false
	}
	rest := name[len(PluginSkillPrefix):]
	at := strings.Index(rest, ":")
	if at <= 0 || at == len(rest)-1 || strings.Contains(rest[at+1:], ":") {
		return "", "", false
	}
	return rest[:at], rest[at+1:], true
}

// homeDir is the user's home directory, or "" when it cannot be read.
func homeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}
