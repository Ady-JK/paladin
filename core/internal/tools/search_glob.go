package tools

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

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

// ---- glob ----

type GlobTool struct{}

func (GlobTool) Name() string       { return "glob" }
func (GlobTool) ParallelSafe() bool { return true }

func (GlobTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "glob",
		Description: "Find files by name pattern under a folder, newest first. ** spans folders, * and ? stay within " +
			"one, {a,b} picks alternatives; a pattern without / matches the file name at any depth. Respects " +
			".gitignore and skips .git and node_modules. Returns one absolute path per line. Results are " +
			"truncated past 50 KiB or the path limit: when truncated is true, narrow the pattern or path.",
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
	output, cut := cutSearchOutput(out.String())
	return searchJSON(SearchResponse{Path: root, Scope: scope, Engine: engine, Files: len(hits), Truncated: truncated || cut, Output: output})
}
