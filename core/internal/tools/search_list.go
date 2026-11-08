package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ---- list ----

type ListTool struct{}

func (ListTool) Name() string       { return "list" }
func (ListTool) ParallelSafe() bool { return true }

func (ListTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "list",
		Description: "Show a folder as an indented tree, folders first with a trailing / and the number of files " +
			"under them. Respects .gitignore and skips .git and node_modules; empty folders are not shown. " +
			"Use it to get oriented before reading files. Entries are truncated past 50 KiB or the entry " +
			"limit: when truncated is true, list a deeper path instead.",
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
	output, cut := cutSearchOutput(out.String())
	return searchJSON(SearchResponse{Path: root, Scope: scope, Engine: engine, Files: top.count, Truncated: truncated || cut, Output: output})
}
