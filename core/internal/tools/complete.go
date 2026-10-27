// Package tools: complete.go powers `@` mentions in chat.
//
// CompleteFiles lists files and folders under the workspace (or its
// parent chain for `../` queries) for the composer's autocomplete
// popup. It prefers `rg --files --hidden` so .gitignore is honored;
// when ripgrep is missing it falls back to a directory walk with a
// minimal .gitignore matcher. Either way gitignored paths are never
// suggested. File rows render Linux-style chat paths (`src/foo.ts`);
// every row also carries the OS-specific absolute path, which is what
// the composer inserts into the message so the agent sees exactly
// which file or folder was tagged.
package tools

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

const (
	// DefaultCompleteLimit caps popup rows per keystroke.
	DefaultCompleteLimit = 20
	// MaxCompleteLimit is the hard ceiling a caller can request.
	MaxCompleteLimit = 50
	// completeScanCap bounds how many files are scanned before
	// ranking, so a huge tree cannot stall the composer.
	completeScanCap = 20000
	// completeDirCap bounds immediate-subdirectory rows per query.
	completeDirCap = 30
)

// FileEntry is one completion row: Path is the Linux-style chat path
// (`src/foo.ts`, `../sib/file`, `src/sub/` for folders),
// ResolvedPath is the OS-specific absolute path the composer inserts
// into the message, Outside flags workspace escapes.
type FileEntry struct {
	Path         string `json:"path"`
	Display      string `json:"display"`
	ResolvedPath string `json:"resolvedPath"`
	Outside      bool   `json:"outside"`
	IsDir        bool   `json:"isDir"`
}

// CompleteFiles suggests files and folders for a partial chat path.
// An empty query lists the workspace root. Queries starting with `..`
// search from the workspace parent so outside files and folders
// complete too. When the query itself resolves to an existing file or
// folder it leads the results, so a fully typed `@path` still tags.
func CompleteFiles(workspace, query string, limit int) ([]FileEntry, error) {
	if strings.TrimSpace(workspace) == "" {
		return nil, ArgsError("no workspace to complete against")
	}
	if limit <= 0 {
		limit = DefaultCompleteLimit
	}
	if limit > MaxCompleteLimit {
		limit = MaxCompleteLimit
	}
	slash := strings.ReplaceAll(strings.TrimSpace(query), "\\", "/")
	anchor := workspace
	if slash == ".." || strings.HasPrefix(slash, "../") {
		anchor = filepath.Dir(workspace)
	}
	files, err := listCandidateFiles(anchor)
	if err != nil {
		return nil, err
	}
	q := strings.ToLower(slash)
	frag := q
	dirPart := ""
	if i := strings.LastIndex(q, "/"); i >= 0 {
		dirPart, frag = q[:i], q[i+1:]
	}
	type ranked struct {
		entry FileEntry
		score int
	}
	var out []ranked
	push := func(abs, chat string, isDir bool, score int) {
		abs = cleanPath(abs)
		if isDir {
			chat = strings.TrimSuffix(chat, "/") + "/"
		}
		outside := markScopeOutside(abs, workspace)
		out = append(out, ranked{
			entry: FileEntry{
				Path:         chat,
				Display:      ChatDisplayShort(chat),
				ResolvedPath: abs,
				Outside:      outside,
				IsDir:        isDir,
			},
			score: score,
		})
	}
	// The exact path first: a fully typed @path tags even with no
	// popup interaction.
	if slash != "" {
		if abs, _, err := ResolveChatPath(slash, workspace); err == nil {
			if info, err := os.Stat(abs); err == nil {
				chat := ToChatPath(abs, workspace)
				push(abs, chat, info.IsDir(), -1)
			}
		}
	}
	for _, abs := range files {
		chat := ToChatPath(abs, workspace)
		lower := strings.ToLower(chat)
		score := -1
		switch {
		case q == "" || lower == q:
			score = 0
		case strings.HasPrefix(lower, q):
			score = 1
		case frag != "" && strings.HasPrefix(baseName(lower), frag):
			score = 2
		case frag != "" && strings.Contains(lower, frag):
			score = 3
		case q != "" && strings.Contains(lower, q):
			score = 4
		}
		if score < 0 {
			continue
		}
		push(abs, chat, false, score)
	}
	// Folders: the query's base directory itself plus its immediate
	// subdirectories, so `@src/` offers the folder to tag.
	ign := cachedIgnoreMatcher(anchor)
	baseInput := dirPart
	if slash == ".." {
		baseInput = ".."
		frag = ""
	}
	if baseInput == "" {
		baseInput = "."
	}
	if baseAbs, _, err := ResolveChatPath(baseInput, workspace); err == nil {
		if info, err := os.Stat(baseAbs); err == nil && info.IsDir() && !ign.ignored(cleanPath(baseAbs), true) {
			if dirPart != "" && frag == "" {
				push(baseAbs, ToChatPath(baseAbs, workspace), true, 0)
			}
			if entries, err := os.ReadDir(baseAbs); err == nil {
				added := 0
				for _, e := range entries {
					if !e.IsDir() || added >= completeDirCap {
						continue
					}
					name := strings.ToLower(e.Name())
					score := -1
					switch {
					case frag == "":
						score = 1
					case strings.HasPrefix(name, frag):
						score = 1
					case strings.Contains(name, frag):
						score = 3
					}
					if score < 0 {
						continue
					}
					sub := filepath.Join(baseAbs, e.Name())
					if ign.ignored(cleanPath(sub), true) {
						continue
					}
					push(sub, ToChatPath(sub, workspace), true, score)
					added++
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score < out[j].score
		}
		if len(out[i].entry.Path) != len(out[j].entry.Path) {
			return len(out[i].entry.Path) < len(out[j].entry.Path)
		}
		return out[i].entry.Path < out[j].entry.Path
	})
	seen := map[string]bool{}
	entries := make([]FileEntry, 0, len(out))
	for _, r := range out {
		if seen[r.entry.Path] {
			continue
		}
		seen[r.entry.Path] = true
		entries = append(entries, r.entry)
		if len(entries) >= limit {
			break
		}
	}
	return entries, nil
}

// markScopeOutside reports whether abs escaped the workspace.
func markScopeOutside(abs, workspace string) bool {
	_, scope := markScope(abs, workspace)
	return scope == ScopeOutside
}

// ignoreCache memoizes matchers per anchor: the .gitignore discovery
// walk runs once per directory instead of once per keystroke.
var (
	ignoreCacheMu sync.Mutex
	ignoreCache   = map[string]*ignoreMatcher{}
)

func cachedIgnoreMatcher(anchor string) *ignoreMatcher {
	key := cleanPath(anchor)
	ignoreCacheMu.Lock()
	defer ignoreCacheMu.Unlock()
	if m, ok := ignoreCache[key]; ok {
		return m
	}
	m := newIgnoreMatcher(anchor)
	ignoreCache[key] = m
	return m
}

func baseName(slash string) string {
	if i := strings.LastIndex(slash, "/"); i >= 0 {
		return slash[i+1:]
	}
	return slash
}

// listCandidateFiles lists files under root via ripgrep (gitignore
// honored, hidden included, .git excluded), falling back to a walk
// with a minimal .gitignore matcher when rg is missing.
func listCandidateFiles(root string) ([]string, error) {
	if rg, err := exec.LookPath("rg"); err == nil {
		if files, ok := rgFiles(rg, root); ok {
			return files, nil
		}
	}
	return walkFiles(root)
}

// rgFiles runs `rg --files --hidden` (gitignore still applies) and
// joins the slash-separated rows onto root.
func rgFiles(rg, root string) ([]string, bool) {
	cmd := exec.Command(rg, "--files", "--hidden", "--glob", "!.git/", "--", root)
	out, err := cmd.Output()
	if err != nil {
		return nil, false
	}
	var files []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		line = strings.ReplaceAll(line, "\\", "/")
		abs := line
		if !isChatAbs(line) {
			abs = filepath.Join(root, filepath.FromSlash(line))
		}
		files = append(files, cleanPath(abs))
		if len(files) >= completeScanCap {
			break
		}
	}
	return files, true
}

// walkFiles is the no-ripgrep fallback: recursive walk honoring
// .gitignore files between root and each candidate.
func walkFiles(root string) ([]string, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, ArgsError(fmt.Sprintf("cannot list %q: %s", root, err))
	}
	if !info.IsDir() {
		return nil, ArgsError(fmt.Sprintf("%q is not a directory", root))
	}
	ign := newIgnoreMatcher(root)
	var files []string
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if name == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if len(files) >= completeScanCap {
			return filepath.SkipAll
		}
		cleaned := cleanPath(path)
		if ign.ignored(cleaned, false) {
			return nil
		}
		files = append(files, cleaned)
		return nil
	})
	if err != nil {
		return nil, ArgsError(fmt.Sprintf("cannot list %q: %s", root, err))
	}
	return files, nil
}

// ignoreRule is one .gitignore line bound to the directory holding
// the .gitignore file. anchored rules match from that directory;
// bare basenames match at any depth.
type ignoreRule struct {
	dir      string
	pattern  string
	dirOnly  bool
	anchored bool
	neg      bool
}

// ignoreMatcher applies .gitignore files found under root.
type ignoreMatcher struct {
	root  string
	rules []ignoreRule
}

func newIgnoreMatcher(root string) *ignoreMatcher {
	m := &ignoreMatcher{root: cleanPath(root)}
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != ".gitignore" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		dir := cleanPath(filepath.Dir(path))
		for _, line := range strings.Split(string(data), "\n") {
			pat, dirOnly, anchored, neg, ok := parseIgnoreLine(line)
			if !ok {
				continue
			}
			m.rules = append(m.rules, ignoreRule{dir: dir, pattern: pat, dirOnly: dirOnly, anchored: anchored, neg: neg})
		}
		return nil
	})
	return m
}

// parseIgnoreLine trims one .gitignore line to (pattern, dirOnly,
// anchored, negated, ok).
func parseIgnoreLine(line string) (string, bool, bool, bool, bool) {
	line = strings.TrimSuffix(line, "\r")
	if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
		return "", false, false, false, false
	}
	// An escaped leading # or ! is literal.
	if strings.HasPrefix(line, "\\#") || strings.HasPrefix(line, "\\!") {
		line = line[1:]
		return strings.Trim(line, "/"), false, strings.Contains(line, "/"), false, true
	}
	neg := false
	if strings.HasPrefix(line, "!") {
		neg = true
		line = line[1:]
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return "", false, false, false, false
	}
	// A slash anywhere (leading included) anchors the pattern to the
	// .gitignore's own directory; a trailing slash is dir-only.
	anchored := strings.Contains(line, "/")
	dirOnly := strings.HasSuffix(line, "/")
	line = strings.Trim(strings.TrimPrefix(line, "/"), "/")
	if line == "" {
		return "", false, false, false, false
	}
	return line, dirOnly, anchored, neg, true
}

// ignored reports whether abs (a file, or dir when isDir) is ignored.
// Later files and deeper .gitignores override earlier ones; a final
// negation re-includes.
func (m *ignoreMatcher) ignored(abs string, isDir bool) bool {
	ignored := false
	slashAbs := filepath.ToSlash(abs)
	for _, r := range m.rules {
		rel := relSlash(slashAbs, filepath.ToSlash(r.dir))
		if rel == "" {
			continue
		}
		if matchIgnoreRule(r, rel, isDir) {
			ignored = !r.neg
		}
	}
	return ignored
}

func relSlash(abs, dir string) string {
	if abs == dir {
		return "."
	}
	if strings.HasPrefix(abs, dir+"/") {
		return abs[len(dir)+1:]
	}
	return ""
}

// matchIgnoreRule matches a slash-relative path against one rule:
// bare basenames hit any level, anchored rules match from the
// .gitignore's directory, both with * ? ** wildcards. A match on a
// parent directory ignores the whole subtree below it.
func matchIgnoreRule(r ignoreRule, rel string, isDir bool) bool {
	pat := r.pattern
	if !r.anchored {
		if ok, _ := wildcardMatch(pat, baseName(rel)); ok {
			return !r.dirOnly || isDir
		}
		// A directory matched by basename ignores its subtree:
		// compare each parent's own name (parents are dirs).
		for parent := range parentSegments(rel) {
			if ok, _ := wildcardMatch(pat, baseName(parent)); ok {
				return true
			}
		}
		return false
	}
	if ok, _ := wildcardMatch(pat, rel); ok {
		return !r.dirOnly || isDir
	}
	for parent := range parentSegments(rel) {
		if ok, _ := wildcardMatch(pat, parent); ok {
			return true
		}
	}
	return false
}

// parentSegments yields rel's parent directories, nearest first.
func parentSegments(rel string) <-chan string {
	ch := make(chan string, 4)
	go func() {
		defer close(ch)
		for {
			i := strings.LastIndex(rel, "/")
			if i <= 0 {
				return
			}
			rel = rel[:i]
			ch <- rel
		}
	}()
	return ch
}

// wildcardMatch matches with * ? and ** (any depth, incl. slashes).
func wildcardMatch(pattern, name string) (bool, error) {
	px, nx := splitSeg(pattern), splitSeg(name)
	return matchSegs(px, nx)
}

func splitSeg(s string) []string { return strings.Split(s, "/") }

func matchSegs(px, nx []string) (bool, error) {
	if len(px) == 0 {
		return len(nx) == 0, nil
	}
	if px[0] == "**" {
		for i := 0; i <= len(nx); i++ {
			if ok, _ := matchSegs(px[1:], nx[i:]); ok {
				return true, nil
			}
		}
		return false, nil
	}
	if len(nx) == 0 {
		return false, nil
	}
	ok, err := segMatch(px[0], nx[0])
	if err != nil || !ok {
		return ok, err
	}
	return matchSegs(px[1:], nx[1:])
}

// segMatch matches one path segment with * and ? (never /).
func segMatch(pattern, name string) (bool, error) {
	px, nx := []rune(pattern), []rune(name)
	return segRunes(px, nx)
}

func segRunes(px, nx []rune) (bool, error) {
	if len(px) == 0 {
		return len(nx) == 0, nil
	}
	switch px[0] {
	case '*':
		for i := 0; i <= len(nx); i++ {
			if ok, _ := segRunes(px[1:], nx[i:]); ok {
				return true, nil
			}
		}
		return false, nil
	case '?':
		if len(nx) == 0 {
			return false, nil
		}
		return segRunes(px[1:], nx[1:])
	case '[':
		return false, fmt.Errorf("character classes are not supported in .gitignore patterns")
	default:
		if len(nx) == 0 || px[0] != nx[0] {
			return false, nil
		}
		return segRunes(px[1:], nx[1:])
	}
}
