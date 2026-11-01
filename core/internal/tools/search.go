// search.go holds the read-only search tools: grep (file contents),
// glob (file names) and list (a directory tree). Each prefers ripgrep
// when it is installed and otherwise walks the tree itself: inside a
// git repo the file list comes from git ls-files, so .gitignore is
// honored either way. Symlinks are never followed and VCS and
// dependency folders are skipped, so a search stays inside its root.

package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	grepDefaultLimit = 100
	globDefaultLimit = 200
	listDefaultLimit = 500
	searchMaxLimit   = 1000
	listMaxDepth     = 5
	grepMaxContext   = 5
	// searchMaxFiles bounds one enumeration so a huge tree cannot
	// exhaust memory; the response reports the cut as truncated.
	searchMaxFiles     = 200_000
	searchMaxFileBytes = 1 << 20
	searchMaxLineRunes = 300
	searchTimeout      = 30 * time.Second
)

// searchUseRipgrep switches ripgrep on when it is installed; tests
// flip it to cover the built-in engine on machines that have rg.
var searchUseRipgrep = true

// searchSkipDirs are never descended into by the built-in walk.
var searchSkipDirs = map[string]bool{
	".git": true, ".hg": true, ".svn": true, "node_modules": true,
	".venv": true, "__pycache__": true,
}

// SearchResponse is the reply of grep, glob and list.
type SearchResponse struct {
	Path      string `json:"path"`
	Scope     string `json:"scope"`
	Engine    string `json:"engine"`
	Matches   int    `json:"matches,omitempty"`
	Files     int    `json:"files"`
	Truncated bool   `json:"truncated"`
	Output    string `json:"output"`
}

// searchRoot resolves the optional path argument; empty means the
// workspace. The root must exist.
func searchRoot(tool, input string, ctx *ToolCtx) (string, string, error) {
	if strings.TrimSpace(input) == "" {
		if ctx == nil || ctx.Workspace == "" {
			return "", "", ArgsError(tool + ": path is required when the session has no workspace")
		}
		input = ctx.Workspace
	}
	workspace := ""
	if ctx != nil {
		workspace = ctx.Workspace
	}
	root, scope, err := ResolveScopedPath(tool, "path", input, workspace)
	if err != nil {
		return "", "", err
	}
	if _, err := os.Stat(root); err != nil {
		return "", "", ArgsError(fmt.Sprintf("%s: %s does not exist", tool, root))
	}
	return root, scope, nil
}

func searchContext(ctx *ToolCtx) (context.Context, context.CancelFunc) {
	parent := context.Background()
	if ctx != nil && ctx.Cancel != nil {
		parent = ctx.Cancel
	}
	return context.WithTimeout(parent, searchTimeout)
}

func clampLimit(n, def int) int {
	if n <= 0 {
		return def
	}
	return min(n, searchMaxLimit)
}

func ripgrep() (string, bool) {
	if !searchUseRipgrep {
		return "", false
	}
	path, err := LookCommand("rg")
	return path, err == nil
}

func searchJSON(resp SearchResponse) (string, error) {
	out, err := json.Marshal(resp)
	return string(out), err
}

// fileEntry is one enumerated regular file.
type fileEntry struct {
	path    string
	rel     string
	modTime time.Time
}

// listFiles enumerates the regular files under root, honoring
// .gitignore where it can. truncated reports the searchMaxFiles cut.
func listFiles(ctx context.Context, root string) (files []fileEntry, engine string, truncated bool, err error) {
	info, err := os.Lstat(root)
	if err != nil {
		return nil, "", false, err
	}
	if !info.IsDir() {
		if !info.Mode().IsRegular() {
			return nil, "builtin", false, nil
		}
		return []fileEntry{{path: root, rel: filepath.Base(root), modTime: info.ModTime()}}, "builtin", false, nil
	}
	if rg, ok := ripgrep(); ok {
		rels, cut, err := runLister(ctx, root, rg, "--files", "--hidden", "--no-follow", "--no-messages",
			"--glob", "!.git", "--null", ".")
		if err == nil {
			return statFiles(root, rels), "rg", cut, nil
		}
	}
	if rels, cut, err := gitFiles(ctx, root); err == nil {
		return statFiles(root, rels), "builtin", cut, nil
	}
	files, truncated, err = walkSearchFiles(ctx, root)
	return files, "builtin", truncated, err
}

// gitFiles lists tracked and untracked, non-ignored files under root
// when root is inside a git work tree.
func gitFiles(ctx context.Context, root string) ([]string, bool, error) {
	git, err := LookCommand("git")
	if err != nil {
		return nil, false, err
	}
	// Searches run without approval: a repository's fsmonitor command
	// (config, not files) must not run with them.
	safe := []string{"-C", root, "-c", "core.fsmonitor=false"}
	probe := exec.CommandContext(ctx, git, append(safe, "rev-parse", "--is-inside-work-tree")...)
	probe.Env = ChildEnv()
	if out, err := probe.Output(); err != nil || strings.TrimSpace(string(out)) != "true" {
		return nil, false, errors.New("not a git work tree")
	}
	return runLister(ctx, root, git, append(safe, "ls-files", "--cached", "--others", "--exclude-standard", "-z")...)
}

// runLister runs a NUL-separated file lister in root and collects at
// most searchMaxFiles relative paths.
func runLister(ctx context.Context, root, bin string, args ...string) ([]string, bool, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = root
	cmd.Env = ChildEnv()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, false, err
	}
	if err := cmd.Start(); err != nil {
		return nil, false, err
	}
	var rels []string
	truncated := false
	scan := bufio.NewScanner(stdout)
	scan.Buffer(make([]byte, 64*1024), 1<<20)
	scan.Split(splitNUL)
	for scan.Scan() {
		if len(rels) == searchMaxFiles {
			truncated = true
			break
		}
		rel := filepath.FromSlash(strings.TrimPrefix(scan.Text(), "./"))
		if rel != "" && !skippedPath(rel) {
			rels = append(rels, rel)
		}
	}
	if truncated {
		_ = cmd.Process.Kill()
	}
	_, _ = io.Copy(io.Discard, stdout)
	err = cmd.Wait()
	if truncated {
		err = nil
	}
	// rg exits 1 when it lists nothing; that is an empty tree.
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 && len(rels) == 0 && filepath.Base(bin) != "git" {
		err = nil
	}
	return rels, truncated, err
}

func splitNUL(data []byte, atEOF bool) (int, []byte, error) {
	if i := bytes.IndexByte(data, 0); i >= 0 {
		return i + 1, data[:i], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// skippedPath reports whether rel runs through a skipped folder; git
// can list a checked-in node_modules, which is still noise.
func skippedPath(rel string) bool {
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if searchSkipDirs[part] {
			return true
		}
	}
	return false
}

// statFiles keeps the regular files among rels; symlinks and vanished
// paths drop out.
func statFiles(root string, rels []string) []fileEntry {
	out := make([]fileEntry, 0, len(rels))
	for _, rel := range rels {
		path := filepath.Join(root, rel)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		out = append(out, fileEntry{path: path, rel: rel, modTime: info.ModTime()})
	}
	return out
}

func walkSearchFiles(ctx context.Context, root string) ([]fileEntry, bool, error) {
	var out []fileEntry
	errFull := errors.New("full")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == root {
				return err
			}
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if path != root && searchSkipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if len(out) == searchMaxFiles {
			return errFull
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return nil //nolint:nilerr // a file that vanished mid-walk is skipped
		}
		rel, _ := filepath.Rel(root, path)
		out = append(out, fileEntry{path: path, rel: rel, modTime: info.ModTime()})
		return nil
	})
	if errors.Is(err, errFull) {
		return out, true, nil
	}
	return out, false, err
}

// globRegexp compiles a gitignore-style glob: ** spans folders, * and ?
// stay within one, {a,b} picks alternatives and [...] is a class.
func globRegexp(pattern string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	depth := 0
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		switch {
		case c == '*' && strings.HasPrefix(pattern[i:], "**/"):
			b.WriteString("(?:.*/)?")
			i += 2
		case c == '*' && strings.HasPrefix(pattern[i:], "**"):
			b.WriteString(".*")
			i++
		case c == '*':
			b.WriteString("[^/]*")
		case c == '?':
			b.WriteString("[^/]")
		case c == '[':
			end := strings.IndexByte(pattern[i+1:], ']')
			if end < 0 {
				return nil, fmt.Errorf("unclosed [ in %q", pattern)
			}
			class := pattern[i+1 : i+1+end]
			if strings.HasPrefix(class, "!") {
				class = "^" + class[1:]
			}
			b.WriteString("[" + class + "]")
			i += end + 1
		case c == '{':
			depth++
			b.WriteString("(?:")
		case c == '}' && depth > 0:
			depth--
			b.WriteString(")")
		case c == ',' && depth > 0:
			b.WriteString("|")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	if depth != 0 {
		return nil, fmt.Errorf("unclosed { in %q", pattern)
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

// globMatches tests a slash-separated relative path. A pattern without
// a slash matches the file name at any depth, like .gitignore.
func globMatches(re *regexp.Regexp, pattern, rel string) bool {
	rel = filepath.ToSlash(rel)
	if !strings.Contains(pattern, "/") {
		rel = rel[strings.LastIndexByte(rel, '/')+1:]
	}
	return re.MatchString(rel)
}

func compileGlob(tool, pattern string) (*regexp.Regexp, error) {
	re, err := globRegexp(filepath.ToSlash(strings.TrimSpace(pattern)))
	if err != nil {
		return nil, ArgsError(fmt.Sprintf("%s: bad glob: %v", tool, err))
	}
	return re, nil
}

func clipLine(s string) string {
	s = strings.TrimRight(s, "\r\n")
	if r := []rune(s); len(r) > searchMaxLineRunes {
		return string(r[:searchMaxLineRunes]) + truncatedMarker
	}
	return s
}

// ---- grep ----

type GrepTool struct{}

func (GrepTool) Name() string       { return "grep" }
func (GrepTool) ParallelSafe() bool { return true }

func (GrepTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "grep",
		Description: "Search file contents with a regular expression (RE2/ripgrep syntax) under a folder or in one file. " +
			"Respects .gitignore and skips binary files, .git and node_modules. Returns matching lines grouped by " +
			"absolute file path with line numbers (\"12: text\"; context lines use \"12- text\"). " +
			"Use grep instead of running grep or rg through the shell.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern":     map[string]any{"type": "string", "description": "The regular expression to search for."},
				"path":        map[string]any{"type": "string", "description": "Absolute folder or file to search. Defaults to the workspace."},
				"glob":        map[string]any{"type": "string", "description": "Only search files matching this glob, e.g. \"*.go\" or \"src/**/*.{ts,tsx}\"."},
				"ignore_case": map[string]any{"type": "boolean", "description": "Match case-insensitively."},
				"output":      map[string]any{"type": "string", "enum": []string{"content", "files"}, "description": "content (default) returns matching lines; files returns only the paths of matching files."},
				"context":     map[string]any{"type": "integer", "minimum": 0, "maximum": grepMaxContext, "description": "Lines of context before and after each match."},
				"limit":       map[string]any{"type": "integer", "minimum": 1, "maximum": searchMaxLimit, "description": fmt.Sprintf("Most matches (or files) to return. Defaults to %d.", grepDefaultLimit)},
			},
			"required": []string{"pattern"},
		},
	}
}

type grepArgs struct {
	Pattern    string `json:"pattern"`
	Path       string `json:"path"`
	Glob       string `json:"glob"`
	IgnoreCase bool   `json:"ignore_case"`
	Output     string `json:"output"`
	Context    int    `json:"context"`
	Limit      int    `json:"limit"`
}

// grepHit is one matching or context line.
type grepHit struct {
	line    int
	text    string
	context bool
}

type grepFile struct {
	path string
	hits []grepHit
}

func (GrepTool) Call(raw json.RawMessage, ctx *ToolCtx) (string, error) {
	var args grepArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", ArgsError(fmt.Sprintf("grep: the arguments are not valid JSON: %v", err))
	}
	if args.Pattern == "" {
		return "", ArgsError("grep: pattern is required")
	}
	if args.Output != "" && args.Output != "content" && args.Output != "files" {
		return "", ArgsError(`grep: output must be "content" or "files"`)
	}
	expr := args.Pattern
	if args.IgnoreCase {
		expr = "(?i)" + expr
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return "", ArgsError(fmt.Sprintf("grep: bad pattern: %v", err))
	}
	var glob *regexp.Regexp
	if strings.TrimSpace(args.Glob) != "" {
		if glob, err = compileGlob("grep", args.Glob); err != nil {
			return "", err
		}
	}
	root, scope, err := searchRoot("grep", args.Path, ctx)
	if err != nil {
		return "", err
	}
	args.Context = max(0, min(args.Context, grepMaxContext))
	limit := clampLimit(args.Limit, grepDefaultLimit)
	cctx, cancel := searchContext(ctx)
	defer cancel()

	files, engine, truncated, err := listFiles(cctx, root)
	if err != nil {
		return "", WithContent(string(mustJSON(map[string]string{"error": err.Error()})), "grep: "+err.Error())
	}
	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
	var found []grepFile
	matches := 0
	for _, f := range files {
		if cctx.Err() != nil {
			truncated = true
			break
		}
		if glob != nil && !globMatches(glob, args.Glob, f.rel) {
			continue
		}
		left := limit - matches
		if args.Output == "files" {
			left = 1
		}
		hits, n, more := grepFileHits(f.path, re, args.Context, left)
		if n == 0 {
			continue
		}
		found = append(found, grepFile{path: f.path, hits: hits})
		matches += n
		if args.Output == "files" {
			if len(found) == limit {
				truncated = truncated || f.path != files[len(files)-1].path
				break
			}
			continue
		}
		if more || matches >= limit {
			truncated = truncated || more || f.path != files[len(files)-1].path
			break
		}
	}
	var out strings.Builder
	for _, f := range found {
		if args.Output == "files" {
			out.WriteString(f.path + "\n")
			continue
		}
		out.WriteString(f.path + "\n")
		last := 0
		for _, h := range f.hits {
			if last != 0 && h.line > last+1 {
				out.WriteString("  --\n")
			}
			sep := ":"
			if h.context {
				sep = "-"
			}
			fmt.Fprintf(&out, "  %d%s %s\n", h.line, sep, h.text)
			last = h.line
		}
	}
	resp := SearchResponse{Path: root, Scope: scope, Engine: engine, Files: len(found), Truncated: truncated, Output: out.String()}
	if args.Output != "files" {
		resp.Matches = matches
	}
	return searchJSON(resp)
}

// grepFileHits scans one file for up to limit matches. more reports a
// further match past the limit. Big and binary files are skipped.
func grepFileHits(path string, re *regexp.Regexp, context, limit int) (hits []grepHit, matches int, more bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() > searchMaxFileBytes {
		return nil, 0, false
	}
	data, err := io.ReadAll(f)
	if err != nil || bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0 {
		return nil, 0, false
	}
	lines := strings.Split(string(data), "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	shown := map[int]bool{}
	for i, line := range lines {
		if !re.MatchString(line) {
			continue
		}
		if matches == limit {
			return hits, matches, true
		}
		matches++
		for j := max(0, i-context); j <= min(len(lines)-1, i+context); j++ {
			if shown[j] {
				if j == i {
					for k := range hits {
						if hits[k].line == j+1 {
							hits[k].context = false
						}
					}
				}
				continue
			}
			shown[j] = true
			hits = append(hits, grepHit{line: j + 1, text: clipLine(lines[j]), context: j != i})
		}
	}
	return hits, matches, false
}

func mustJSON(v any) []byte {
	out, _ := json.Marshal(v)
	return out
}

// ---- glob ----

type GlobTool struct{}

func (GlobTool) Name() string       { return "glob" }
func (GlobTool) ParallelSafe() bool { return true }

func (GlobTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "glob",
		Description: "Find files by name pattern under a folder, newest first. ** spans folders, * and ? stay within " +
			"one, {a,b} picks alternatives; a pattern without / matches the file name at any depth. Respects " +
			".gitignore and skips .git and node_modules. Returns one absolute path per line.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{"type": "string", "description": "The glob, e.g. \"**/*.go\", \"*_test.go\" or \"src/**/index.{ts,tsx}\"."},
				"path":    map[string]any{"type": "string", "description": "Absolute folder to search. Defaults to the workspace."},
				"limit":   map[string]any{"type": "integer", "minimum": 1, "maximum": searchMaxLimit, "description": fmt.Sprintf("Most paths to return. Defaults to %d.", globDefaultLimit)},
			},
			"required": []string{"pattern"},
		},
	}
}

func (GlobTool) Call(raw json.RawMessage, ctx *ToolCtx) (string, error) {
	var args struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
		Limit   int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", ArgsError(fmt.Sprintf("glob: the arguments are not valid JSON: %v", err))
	}
	if strings.TrimSpace(args.Pattern) == "" {
		return "", ArgsError("glob: pattern is required")
	}
	re, err := compileGlob("glob", args.Pattern)
	if err != nil {
		return "", err
	}
	root, scope, err := searchRoot("glob", args.Path, ctx)
	if err != nil {
		return "", err
	}
	limit := clampLimit(args.Limit, globDefaultLimit)
	cctx, cancel := searchContext(ctx)
	defer cancel()
	files, engine, truncated, err := listFiles(cctx, root)
	if err != nil {
		return "", WithContent(string(mustJSON(map[string]string{"error": err.Error()})), "glob: "+err.Error())
	}
	var hits []fileEntry
	for _, f := range files {
		if globMatches(re, args.Pattern, f.rel) {
			hits = append(hits, f)
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if !hits[i].modTime.Equal(hits[j].modTime) {
			return hits[i].modTime.After(hits[j].modTime)
		}
		return hits[i].path < hits[j].path
	})
	if len(hits) > limit {
		hits, truncated = hits[:limit], true
	}
	var out strings.Builder
	for _, f := range hits {
		out.WriteString(f.path + "\n")
	}
	return searchJSON(SearchResponse{Path: root, Scope: scope, Engine: engine, Files: len(hits), Truncated: truncated, Output: out.String()})
}

// ---- list ----

type ListTool struct{}

func (ListTool) Name() string       { return "list" }
func (ListTool) ParallelSafe() bool { return true }

func (ListTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "list",
		Description: "Show a folder as an indented tree, folders first with a trailing / and the number of files " +
			"under them. Respects .gitignore and skips .git and node_modules; empty folders are not shown. " +
			"Use it to get oriented before reading files.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":  map[string]any{"type": "string", "description": "Absolute folder to list. Defaults to the workspace."},
				"depth": map[string]any{"type": "integer", "minimum": 1, "maximum": listMaxDepth, "description": "How many levels to expand. Defaults to 2."},
				"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": searchMaxLimit, "description": fmt.Sprintf("Most entries to show. Defaults to %d.", listDefaultLimit)},
			},
		},
	}
}

// treeNode is one folder of the derived tree.
type treeNode struct {
	dirs  map[string]*treeNode
	files []string
	count int
}

func (ListTool) Call(raw json.RawMessage, ctx *ToolCtx) (string, error) {
	var args struct {
		Path  string `json:"path"`
		Depth int    `json:"depth"`
		Limit int    `json:"limit"`
	}
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", ArgsError(fmt.Sprintf("list: the arguments are not valid JSON: %v", err))
		}
	}
	root, scope, err := searchRoot("list", args.Path, ctx)
	if err != nil {
		return "", err
	}
	if info, err := os.Stat(root); err == nil && !info.IsDir() {
		return "", ArgsError(fmt.Sprintf("list: %s is a file; use read", root))
	}
	depth := args.Depth
	if depth <= 0 {
		depth = 2
	}
	depth = min(depth, listMaxDepth)
	limit := clampLimit(args.Limit, listDefaultLimit)
	cctx, cancel := searchContext(ctx)
	defer cancel()
	files, engine, truncated, err := listFiles(cctx, root)
	if err != nil {
		return "", WithContent(string(mustJSON(map[string]string{"error": err.Error()})), "list: "+err.Error())
	}
	top := &treeNode{dirs: map[string]*treeNode{}}
	for _, f := range files {
		parts := strings.Split(f.rel, string(filepath.Separator))
		node := top
		node.count++
		for _, dir := range parts[:len(parts)-1] {
			next := node.dirs[dir]
			if next == nil {
				next = &treeNode{dirs: map[string]*treeNode{}}
				node.dirs[dir] = next
			}
			next.count++
			node = next
		}
		node.files = append(node.files, parts[len(parts)-1])
	}
	var out strings.Builder
	out.WriteString(strings.TrimSuffix(root, string(filepath.Separator)) + string(filepath.Separator) + "\n")
	shown := 0
	var walk func(n *treeNode, level int) bool
	walk = func(n *treeNode, level int) bool {
		indent := strings.Repeat("  ", level)
		names := make([]string, 0, len(n.dirs))
		for name := range n.dirs {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if shown == limit {
				return false
			}
			shown++
			child := n.dirs[name]
			noun := "files"
			if child.count == 1 {
				noun = "file"
			}
			fmt.Fprintf(&out, "%s%s/ (%d %s)\n", indent, name, child.count, noun)
			if level < depth && !walk(child, level+1) {
				return false
			}
		}
		sort.Strings(n.files)
		for _, name := range n.files {
			if shown == limit {
				return false
			}
			shown++
			out.WriteString(indent + name + "\n")
		}
		return true
	}
	if !walk(top, 1) {
		truncated = true
	}
	return searchJSON(SearchResponse{Path: root, Scope: scope, Engine: engine, Files: top.count, Truncated: truncated, Output: out.String()})
}
