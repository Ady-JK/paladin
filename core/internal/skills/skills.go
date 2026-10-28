// Package skills loads agent skills from SKILL.md files.
//
// A skill is a directory holding one skill file:
//
//	config/skills/<skill-name>/SKILL.md            (global, under the data dir)
//	<workspace>/.paladin/skills/<skill-name>/SKILL.md  (workspace-local)
//
// The file name matches case-insensitively, so skill.md works too, and
// the directory name is the skill name. Two roots are scanned: the
// shared config dir ("global") and the workspace dir ("workspace"). A
// workspace skill wins when both roots define the same name.
//
// Skills never touch the composer text: the frontend sends the skill
// name alongside the message, and the runtime injects the file content
// into the model-facing message (and the persisted transcript) before
// the LLM call.
package skills

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	SourceGlobal    = "global"
	SourceWorkspace = "workspace"

	// SkillFileName is matched case-insensitively (SKILL.md, skill.md).
	SkillFileName = "skill.md"

	// MaxContentBytes caps one skill file so a runaway file cannot
	// blow the model's context window.
	MaxContentBytes = 64 * 1024
)

// Skill is one discovered skill. List leaves Content empty and reports
// the Description instead; Load fills Content.
type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"`
	Path        string `json:"path"`
	Content     string `json:"content,omitempty"`
}

// GlobalDir is the shared skills root under the data dir.
func GlobalDir(dataDir string) string {
	return filepath.Join(dataDir, "config", "skills")
}

// WorkspaceDir is the workspace-local skills root. Empty cwd means no
// workspace root.
func WorkspaceDir(cwd string) string {
	if strings.TrimSpace(cwd) == "" {
		return ""
	}
	return filepath.Join(cwd, ".paladin", "skills")
}

// ValidName reports whether name is usable as a skill lookup: a bare
// directory name, never a path.
func ValidName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	return !strings.ContainsAny(name, "/\\")
}

// List discovers skills from both roots. A workspace skill shadows a
// global one of the same name (case-insensitively). Results sort by
// name.
func List(dataDir, cwd string) []Skill {
	byName := map[string]Skill{}
	for _, root := range []struct {
		dir    string
		source string
	}{
		{GlobalDir(dataDir), SourceGlobal},
		{WorkspaceDir(cwd), SourceWorkspace},
	} {
		if root.dir == "" {
			continue
		}
		for _, s := range scanRoot(root.dir, root.source) {
			key := strings.ToLower(s.Name)
			if _, taken := byName[key]; taken && root.source == SourceGlobal {
				continue
			}
			byName[key] = s
		}
	}
	out := make([]Skill, 0, len(byName))
	for _, s := range byName {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Load reads one skill by name (case-insensitive). The workspace root
// wins over the global root.
func Load(dataDir, cwd, name string) (Skill, error) {
	name = strings.TrimSpace(name)
	if !ValidName(name) {
		return Skill{}, fmt.Errorf("parse: invalid skill name %q", name)
	}
	roots := []struct {
		dir    string
		source string
	}{
		{WorkspaceDir(cwd), SourceWorkspace},
		{GlobalDir(dataDir), SourceGlobal},
	}
	for _, root := range roots {
		if root.dir == "" {
			continue
		}
		skill, ok, err := loadFrom(root.dir, name, root.source)
		if err != nil {
			return Skill{}, err
		}
		if ok {
			return skill, nil
		}
	}
	return Skill{}, fmt.Errorf("not found: skill %q not found (add %s/SKILL.md under config/skills/ or .paladin/skills/)", name, name)
}

// Inject builds the model-facing user message: the typed text plus the
// skill content under a "Use this skill" banner. The composer keeps
// only `/skill <name> <text>`; the transcript keeps this expansion.
func Inject(message string, s Skill) string {
	var b strings.Builder
	if strings.TrimSpace(message) != "" {
		b.WriteString(message)
		b.WriteString("\n\n")
	}
	b.WriteString("Use this skill (" + s.Name + "):\n")
	b.WriteString(s.Content)
	return b.String()
}

// scanRoot lists the skills directly under one root directory. A
// missing root simply yields nothing.
func scanRoot(root, source string) []Skill {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []Skill
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		path, content, ok := readSkillFile(filepath.Join(root, e.Name()))
		if !ok || len(content) > MaxContentBytes {
			// Oversize skills fail Load, so the picker must not
			// offer what a turn could never inject.
			continue
		}
		out = append(out, Skill{
			Name:        e.Name(),
			Description: describe(content),
			Source:      source,
			Path:        path,
		})
	}
	return out
}

// loadFrom looks up one skill directory (case-insensitive) under root
// and reads its skill file with content.
func loadFrom(root, name, source string) (Skill, bool, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return Skill{}, false, nil
		}
		return Skill{}, false, fmt.Errorf("io: list skills in %s: %s", root, err)
	}
	dir := ""
	actual := ""
	for _, e := range entries {
		if e.IsDir() && strings.EqualFold(e.Name(), name) {
			dir = filepath.Join(root, e.Name())
			actual = e.Name()
			break
		}
	}
	if dir == "" {
		return Skill{}, false, nil
	}
	path, content, ok := readSkillFile(dir)
	if !ok {
		return Skill{}, false, nil
	}
	if len(content) > MaxContentBytes {
		return Skill{}, false, fmt.Errorf("parse: skill %q exceeds %d bytes", actual, MaxContentBytes)
	}
	return Skill{
		Name:        actual,
		Description: describe(content),
		Source:      source,
		Path:        path,
		Content:     content,
	}, true, nil
}

// readSkillFile finds the skill file inside dir (case-insensitive) and
// reads it. Only the first 64KB+1 byte is read: anything longer fails
// the size cap in the caller.
func readSkillFile(dir string) (string, string, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", "", false
	}
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(e.Name(), SkillFileName) {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := readCapped(path, MaxContentBytes+1)
		if err != nil {
			return "", "", false
		}
		return path, string(data), true
	}
	return "", "", false
}

// readCapped reads at most limit bytes so a runaway file cannot exhaust
// memory; callers compare the length against the cap.
func readCapped(path string, limit int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, int64(limit)))
}

// describe summarizes a skill file: the frontmatter `description:`
// when present, else the first non-empty body line, truncated.
func describe(content string) string {
	body := content
	if fm, rest, ok := splitFrontmatter(content); ok {
		body = rest
		if d := frontmatterValue(fm, "description"); d != "" {
			return truncate(d, 140)
		}
	}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "---" {
			continue
		}
		line = strings.TrimSpace(strings.TrimLeft(line, "#"))
		line = strings.Trim(line, "*_`")
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		return truncate(line, 140)
	}
	return ""
}

// splitFrontmatter cuts a leading `---` block off content, returning
// the block and the rest. ok is false when there is no opening fence
// or no closing fence.
func splitFrontmatter(content string) (fm, rest string, ok bool) {
	lines := strings.Split(content, "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[0]) != "---" {
		return "", "", false
	}
	for i := 1; i < len(lines); i++ {
		fence := strings.TrimSpace(lines[i])
		if fence == "---" || fence == "..." {
			return strings.Join(lines[1:i], "\n"), strings.Join(lines[i+1:], "\n"), true
		}
	}
	return "", "", false
}

// frontmatterValue reads a top-level `key: value` line (case-insensitive
// key), stripping surrounding quotes.
func frontmatterValue(fm, key string) string {
	for _, line := range strings.Split(fm, "\n") {
		name, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(name), key) {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') ||
				(value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}
		return value
	}
	return ""
}

func truncate(text string, n int) string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) <= n {
		return string(runes)
	}
	return strings.TrimRight(string(runes[:n]), " \n\t\r") + "…"
}
