// search.go holds the read-only search tools: grep (file contents),
// glob (file names) and list (a directory tree). Each prefers ripgrep
// when it is installed and otherwise walks the tree itself: inside a
// git repo the file list comes from git ls-files, so .gitignore is
// honored either way. Symlinks are never followed and VCS and
// dependency folders are skipped, so a search stays inside its root.

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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
	// searchMaxOutputBytes caps one rendered answer the way read caps
	// a page: past it the answer is cut at a line boundary and marked
	// truncated, so the model narrows the query instead of drowning
	// the context. Uniform with read's 50 KiB page.
	searchMaxOutputBytes = 50 << 10
	searchTimeout        = 30 * time.Second
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

func clipLine(s string) string {
	s = strings.TrimRight(s, "\r\n")
	if r := []rune(s); len(r) > searchMaxLineRunes {
		return string(r[:searchMaxLineRunes]) + truncatedMarker
	}
	return s
}

// cutSearchOutput caps a rendered search answer at the output budget,
// cutting at a line boundary so no row is torn. It reports whether it
// cut, and names the way out: a narrower path, glob, or pattern.
func cutSearchOutput(out string) (string, bool) {
	if len(out) <= searchMaxOutputBytes {
		return out, false
	}
	cut := strings.LastIndex(out[:searchMaxOutputBytes], "\n")
	if cut <= 0 {
		cut = searchMaxOutputBytes
	}
	return out[:cut] + "\n[truncated at 50 KiB: use a more specific path, glob, or narrower pattern]\n", true
}

func mustJSON(v any) []byte {
	out, _ := json.Marshal(v)
	return out
}
