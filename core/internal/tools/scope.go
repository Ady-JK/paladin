// Package tools: scope.go resolves model-supplied paths.
package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	ScopeInside  = "inside"
	ScopeOutside = "outside"
)

const workspacePrefix = "workspace"

// ResolveScopedPath resolves a model-supplied path under the scoping
// rule. workspace/-prefixed input resolves inside the workspace and
// reports inside; anything else must be an absolute path and reports
// outside. A prefixed path escaping the workspace is rejected.
func ResolveScopedPath(tool, field, input, workspace string) (string, string, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return "", "", ArgsError(fmt.Sprintf("%s: %s is required", tool, field))
	}
	if rest, ok := cutWorkspacePrefix(trimmed); ok {
		if workspace == "" {
			return "", "", ArgsError(fmt.Sprintf("%s: no workspace to resolve %s against", tool, field))
		}
		joined := filepath.Join(workspace, rest)
		cleaned := cleanPath(joined)
		if escapes(cleaned, workspace) {
			return "", "", ArgsError(fmt.Sprintf("%s: %s must stay inside the workspace; use the absolute path for files outside it", tool, field))
		}
		if real, ok := resolveExisting(cleaned); ok {
			if escapes(real, workspace) {
				return "", "", ArgsError(fmt.Sprintf("%s: %s must stay inside the workspace; use the absolute path for files outside it", tool, field))
			}
		}
		return cleaned, ScopeInside, nil
	}
	if !filepath.IsAbs(trimmed) {
		return "", "", ArgsError(fmt.Sprintf("%s: %s must start with %s/ for workspace files or be an absolute path, got %q", tool, field, workspacePrefix, trimmed))
	}
	return cleanPath(trimmed), ScopeOutside, nil
}

func cutWorkspacePrefix(path string) (string, bool) {
	if path == workspacePrefix {
		return "", true
	}
	for _, sep := range []string{"/", "\\"} {
		if rest, ok := strings.CutPrefix(path, workspacePrefix+sep); ok {
			return strings.ReplaceAll(rest, "\\", "/"), true
		}
	}
	return "", false
}

func cleanPath(path string) string {
	abs := path
	vol := filepath.VolumeName(abs)
	rest := abs[len(vol):]
	isAbs := strings.HasPrefix(rest, "/") || strings.HasPrefix(rest, "\\")
	parts := strings.FieldsFunc(rest, func(r rune) bool { return r == '/' || r == '\\' })
	var out []string
	for _, p := range parts {
		switch p {
		case "", ".":
		case "..":
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
		default:
			out = append(out, p)
		}
	}
	joined := strings.Join(out, string(filepath.Separator))
	if isAbs {
		joined = string(filepath.Separator) + joined
	}
	result := vol + joined
	if result == "" {
		return string(filepath.Separator)
	}
	return result
}

func escapes(cleaned, workspace string) bool {
	root := realPath(workspace)
	target := cleanPath(cleaned)
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return true
	}
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func resolveExisting(cleaned string) (string, bool) {
	path := cleaned
	var tail []string
	for {
		if _, err := os.Lstat(path); err == nil {
			break
		}
		parent := filepath.Dir(path)
		if parent == path {
			return "", false
		}
		tail = append(tail, filepath.Base(path))
		path = parent
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", false
	}
	for i := len(tail) - 1; i >= 0; i-- {
		real = filepath.Join(real, tail[i])
	}
	return cleanPath(real), true
}

func realPath(path string) string {
	if abs, err := filepath.EvalSymlinks(path); err == nil {
		return abs
	}
	abs := path
	if !filepath.IsAbs(abs) {
		if cwd, err := os.Getwd(); err == nil {
			abs = filepath.Join(cwd, abs)
		}
	}
	return cleanPath(abs)
}
