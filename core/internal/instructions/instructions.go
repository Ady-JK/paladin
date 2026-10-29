// Package instructions discovers AGENTS.md project instructions, the
// way Codex and OpenCode do: a global file under the data dir, then one
// file per directory from the project root (the nearest ancestor with
// .git) down to the session folder. Without a git root only the folder
// itself is read. In each directory AGENTS.override.md beats AGENTS.md,
// and CLAUDE.md is accepted as a fallback.
package instructions

import (
	"io"
	"os"
	"path/filepath"
	"strings"
)

// MaxBytes caps the combined instructions so a giant file cannot eat
// the context window.
const MaxBytes = 32 * 1024

// Candidates are tried in order in each directory; the first hit wins.
var Candidates = []string{"AGENTS.override.md", "AGENTS.md", "CLAUDE.md"}

// Doc is one discovered instruction file.
type Doc struct {
	Path      string `json:"path"`
	Scope     string `json:"scope"` // "global" or "project"
	Content   string `json:"content"`
	Truncated bool   `json:"truncated,omitempty"`
}

// GlobalPath is the user's global AGENTS.md.
func GlobalPath(dataDir string) string {
	return filepath.Join(dataDir, "config", "AGENTS.md")
}

// ProjectRoot is the nearest ancestor of cwd holding .git, or "" when
// cwd is not inside a repository.
func ProjectRoot(cwd string) string {
	dir := filepath.Clean(cwd)
	for i := 0; i < 64; i++ {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
	return ""
}

// Discover returns the instruction files in prompt order: global, then
// root → cwd. The combined size stays within MaxBytes.
func Discover(dataDir, cwd string) []Doc {
	budget := MaxBytes
	var docs []Doc
	add := func(path, scope string) {
		if budget <= 0 {
			return
		}
		content, truncated, ok := readCapped(path, budget)
		if !ok || strings.TrimSpace(content) == "" {
			return
		}
		budget -= len(content)
		docs = append(docs, Doc{Path: path, Scope: scope, Content: strings.TrimSpace(content), Truncated: truncated})
	}
	if dataDir != "" {
		add(GlobalPath(dataDir), "global")
	}
	if strings.TrimSpace(cwd) == "" {
		return docs
	}
	for _, dir := range chain(cwd) {
		for _, name := range Candidates {
			path := filepath.Join(dir, name)
			if st, err := os.Stat(path); err == nil && !st.IsDir() {
				add(path, "project")
				break
			}
		}
	}
	return docs
}

// chain lists directories from the project root down to cwd.
func chain(cwd string) []string {
	cwd = filepath.Clean(cwd)
	root := ProjectRoot(cwd)
	if root == "" {
		return []string{cwd}
	}
	var dirs []string
	for dir := cwd; ; dir = filepath.Dir(dir) {
		dirs = append(dirs, dir)
		if dir == root || filepath.Dir(dir) == dir {
			break
		}
	}
	for i, j := 0, len(dirs)-1; i < j; i, j = i+1, j-1 {
		dirs[i], dirs[j] = dirs[j], dirs[i]
	}
	return dirs
}

func readCapped(path string, limit int) (string, bool, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false, false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return "", false, false
	}
	if len(data) > limit {
		return strings.ToValidUTF8(string(data[:limit]), "") + "\n[… truncated]", true, true
	}
	return string(data), false, true
}

// Render formats the docs as a system-prompt section; "" when none.
func Render(docs []Doc) string {
	if len(docs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Project instructions (AGENTS.md). Follow them; files deeper in the tree take precedence over earlier ones when they conflict, and direct user instructions beat all of them.\n")
	for _, d := range docs {
		b.WriteString("\n--- ")
		b.WriteString(d.Path)
		b.WriteString(" ---\n")
		b.WriteString(d.Content)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
