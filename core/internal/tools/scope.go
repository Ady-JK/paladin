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

// ResolveScopedPath resolves a model-supplied path. The model always
// passes a full absolute path; bare relative paths (including the old
// workspace/ prefix) are rejected. The absolute path is compared
// against the session workspace internally and reported as inside or
// outside — flagging only, never a rejection: files outside the
// workspace are allowed.
func ResolveScopedPath(tool, field, input, workspace string) (string, string, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return "", "", ArgsError(fmt.Sprintf("%s: %s is required", tool, field))
	}
	if !filepath.IsAbs(trimmed) {
		return "", "", ArgsError(fmt.Sprintf("%s: %s must be a full absolute path, got %q", tool, field, trimmed))
	}
	cleaned := cleanPath(trimmed)
	return cleaned, scopeOf(cleaned, workspace), nil
}

// scopeOf compares an absolute path against the workspace: inside when
// it lands under the workspace root (following symlinks for files
// that exist), outside otherwise. Empty workspace always reports
// outside.
func scopeOf(cleaned, workspace string) string {
	if workspace != "" && !escapes(cleaned, workspace) {
		if real, ok := resolveExisting(cleaned); ok {
			if escapes(real, workspace) {
				return ScopeOutside
			}
		}
		return ScopeInside
	}
	return ScopeOutside
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
