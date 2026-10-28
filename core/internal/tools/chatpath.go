// Package tools: chatpath.go resolves user-typed chat paths.
//
// Chat paths follow one Linux-style rule on every OS: `/` separates
// segments even on Windows. A bare relative path (`src/foo.ts`)
// resolves inside the workspace, `../sibling/file` (or deeper `..`)
// escapes to the workspace's parent chain, `workspace/...` is the
// explicit inside form, and absolute paths (POSIX `/x` or Windows
// `C:/x`, backslashes tolerated) resolve as-is. The scope reports
// whether the target landed inside the workspace or outside it —
// unlike ResolveScopedPath, escaping is allowed here because `@`
// mentions deliberately tag files outside the workspace.
package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ResolveChatPath maps a user-typed chat path to an OS path. It
// returns the cleaned absolute path and ScopeInside/ScopeOutside.
func ResolveChatPath(input, workspace string) (string, string, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return "", "", ArgsError("path is required")
	}
	// Chat paths are Linux-style: normalize separators before parsing
	// so `..\sib\file` and `../sib/file` mean the same everywhere.
	slash := strings.ReplaceAll(trimmed, "\\", "/")

	if strings.HasPrefix(slash, "~/") || slash == "~" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", "", ArgsError("could not resolve the home directory")
		}
		rest := strings.TrimPrefix(slash, "~")
		rest = strings.TrimPrefix(rest, "/")
		p, scope := markScope(filepath.Join(home, filepath.FromSlash(rest)), workspace)
		return p, scope, nil
	}

	var abs string
	if rest, ok := cutWorkspacePrefix(slash); ok {
		if workspace == "" {
			return "", "", ArgsError("no workspace to resolve the path against")
		}
		abs = filepath.Join(workspace, filepath.FromSlash(rest))
		p, scope := markScope(abs, workspace)
		return p, scope, nil
	}
	if isChatAbs(slash) {
		abs = filepath.FromSlash(slash)
		if workspace == "" {
			return cleanPath(abs), ScopeOutside, nil
		}
		p, scope := markScope(abs, workspace)
		return p, scope, nil
	}
	if workspace == "" {
		return "", "", ArgsError(fmt.Sprintf("path %q is relative but there is no workspace; use an absolute path", trimmed))
	}
	abs = filepath.Join(workspace, filepath.FromSlash(slash))
	p, scope := markScope(abs, workspace)
	return p, scope, nil
}

// isChatAbs reports POSIX-absolute (`/x`) or Windows-absolute
// (`C:/x`, `C:x` is not absolute) slash paths.
func isChatAbs(slash string) bool {
	if strings.HasPrefix(slash, "/") {
		return true
	}
	if len(slash) >= 3 && slash[1] == ':' && slash[2] == '/' {
		c := slash[0]
		return ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z')
	}
	return false
}

// markScope cleans abs and reports inside/outside against workspace.
func markScope(abs, workspace string) (string, string) {
	cleaned := cleanPath(abs)
	if workspace != "" && !escapes(cleaned, workspace) {
		return cleaned, ScopeInside
	}
	return cleaned, ScopeOutside
}

// ToChatPath renders an absolute OS path as a chat path: workspace-
// relative (`src/foo.ts`) when inside, `../...` when reachable from
// the workspace parent, otherwise the absolute path with `/`
// separators. It is the inverse of ResolveChatPath for display.
func ToChatPath(absPath, workspace string) string {
	cleaned := cleanPath(absPath)
	if workspace != "" {
		if rel, err := filepath.Rel(workspace, cleaned); err == nil {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(cleaned)
}

// ChatDisplayShort renders a chat path's basename for popup rows.
func ChatDisplayShort(chatPath string) string {
	slash := strings.ReplaceAll(strings.TrimSpace(chatPath), "\\", "/")
	slash = strings.TrimSuffix(slash, "/")
	if i := strings.LastIndex(slash, "/"); i >= 0 {
		return slash[i+1:]
	}
	return slash
}
