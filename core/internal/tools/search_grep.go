package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
)

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
			"Use grep instead of running grep or rg through the shell. Results are truncated past 50 KiB " +
			"or the match limit: when truncated is true, narrow the pattern, path, or glob instead of paging.",
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
	if output, cut := cutSearchOutput(resp.Output); cut {
		resp.Output, resp.Truncated = output, true
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
	shown := map[int]int{}
	for i, line := range lines {
		if !re.MatchString(line) {
			continue
		}
		if matches == limit {
			return hits, matches, true
		}
		matches++
		for j := max(0, i-context); j <= min(len(lines)-1, i+context); j++ {
			if hitIndex, ok := shown[j]; ok {
				if j == i {
					hits[hitIndex].context = false
				}
				continue
			}
			shown[j] = len(hits)
			hits = append(hits, grepHit{line: j + 1, text: clipLine(lines[j]), context: j != i})
		}
	}
	return hits, matches, false
}
