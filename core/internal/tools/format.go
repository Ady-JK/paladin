// format.go formats a file after write or edit with the formatter the
// project already uses. Only formatters that are installed are run, and
// nothing is ever downloaded: gofmt and rustfmt for Go and Rust, ruff or
// black for Python when pyproject.toml (or ruff.toml) configures them,
// and prettier only from the project's own node_modules/.bin. The
// formatter reads the file on stdin inside a read-only sandbox (where
// the OS has one) and its output replaces the file through the same
// conflict-checked write as edit. Files outside the workspace, failures
// and timeouts leave the file as written.

package tools

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Ady-JK/paladin/core/internal/custom"
	"github.com/Ady-JK/paladin/core/internal/sandbox"
)

const formatTimeout = 10 * time.Second

// formatNote tells the model its file changed under it.
const formatNote = "The file was formatted with {{formatter}} after the change; re-read it before further edits so old_string matches."

func formattedNote(name string) string {
	return custom.TextFill("note.formatted", map[string]string{"formatter": name})
}

var prettierExts = map[string]bool{
	".js": true, ".jsx": true, ".mjs": true, ".cjs": true, ".ts": true, ".tsx": true, ".mts": true, ".cts": true,
	".json": true, ".css": true, ".scss": true, ".less": true, ".html": true, ".vue": true, ".svelte": true,
	".md": true, ".mdx": true, ".yaml": true, ".yml": true, ".graphql": true,
}

// formatCommand picks the formatter for path: its name and argv (the
// file arrives on stdin, the result is read from stdout). "" when the
// file type or project has none installed. A formatter the project ships
// runs only if trust allows it (nil trusts); otherwise one installed
// outside the workspace is used, if any.
func formatCommand(path, workspace string, trust func(string) bool) (string, []string) {
	switch ext := strings.ToLower(filepath.Ext(path)); {
	case ext == ".go":
		if bin, err := LookCommand("gofmt"); err == nil {
			return "gofmt", []string{bin}
		}
	case ext == ".rs":
		if bin, err := LookCommand("rustfmt"); err == nil {
			return "rustfmt", []string{bin, "--emit", "stdout", "--edition", "2021"}
		}
	case ext == ".py" || ext == ".pyi":
		switch pythonFormatter(filepath.Dir(path), workspace) {
		case "ruff":
			if bin := projectBin(path, workspace, trust, ".venv/bin/ruff", "venv/bin/ruff"); bin != "" {
				return "ruff", []string{bin, "format", "--no-cache", "--stdin-filename", path, "-"}
			}
		case "black":
			if bin := projectBin(path, workspace, trust, ".venv/bin/black", "venv/bin/black"); bin != "" {
				return "black", []string{bin, "-q", "--stdin-filename", path, "-"}
			}
		}
	case prettierExts[ext]:
		if bin := localBin(path, workspace, trust, "node_modules/.bin/prettier"); bin != "" {
			return "prettier", []string{bin, "--stdin-filepath", path}
		}
	}
	return "", nil
}

// pythonFormatter reads the nearest project config up to the workspace:
// ruff.toml or [tool.ruff] picks ruff, [tool.black] picks black.
func pythonFormatter(dir, workspace string) string {
	for d := range upTo(dir, workspace) {
		for _, name := range []string{"ruff.toml", ".ruff.toml"} {
			if st, err := os.Stat(filepath.Join(d, name)); err == nil && st.Mode().IsRegular() {
				return "ruff"
			}
		}
		data, err := os.ReadFile(filepath.Join(d, "pyproject.toml"))
		if err != nil {
			continue
		}
		text := string(data)
		switch {
		case strings.Contains(text, "[tool.ruff"):
			return "ruff"
		case strings.Contains(text, "[tool.black"):
			return "black"
		}
		return ""
	}
	return ""
}

// projectBin finds a tool in a project virtualenv, else on PATH.
func projectBin(path, workspace string, trust func(string) bool, rels ...string) string {
	if bin := localBin(path, workspace, trust, rels...); bin != "" {
		return bin
	}
	if bin, err := LookCommand(filepath.Base(rels[0])); err == nil {
		return bin
	}
	return ""
}

// localBin finds an executable at one of rels in the file's folder or
// any folder above it up to the workspace root, if trust allows running
// it ("" otherwise).
func localBin(path, workspace string, trust func(string) bool, rels ...string) string {
	for d := range upTo(filepath.Dir(path), workspace) {
		for _, rel := range rels {
			bin := filepath.Join(d, filepath.FromSlash(rel))
			if st, err := os.Stat(bin); err == nil && st.Mode().IsRegular() && st.Mode().Perm()&0o111 != 0 {
				if trust != nil && !trust(ProjectLabel(workspace, bin)) {
					return ""
				}
				return bin
			}
		}
	}
	return ""
}

// ProjectLabel names a tool the project ships by its path in the
// workspace, for the approval card.
func ProjectLabel(workspace, bin string) string {
	if rel, err := filepath.Rel(workspace, bin); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return bin
}

// upTo yields dir and its parents, stopping at workspace.
func upTo(dir, workspace string) func(func(string) bool) {
	return func(yield func(string) bool) {
		root := filepath.Clean(workspace)
		for d := filepath.Clean(dir); ; d = filepath.Dir(d) {
			if !Within(d, root) || !yield(d) || d == root || filepath.Dir(d) == d {
				return
			}
		}
	}
}

// formatFile formats resolved in place when formatting is on and a
// formatter applies; it returns the formatter's name when it changed
// the file. The session keeps seeing the file only if it saw the
// version just written.
func formatFile(ctx *ToolCtx, resolved string) string {
	if ctx == nil || !ctx.FormatOnWrite || ctx.Workspace == "" || scopeOf(resolved, ctx.Workspace) != ScopeInside {
		return ""
	}
	name, argv := formatCommand(resolved, ctx.Workspace, ctx.TrustProject)
	if name == "" {
		return ""
	}
	unlock := lockPath(resolved)
	defer unlock()
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() || info.Size() > MaxFileBytes {
		return ""
	}
	raw, err := os.ReadFile(resolved)
	if err != nil {
		return ""
	}
	out, err := runFormatter(ctx, argv, filepath.Dir(resolved), raw)
	if err != nil || bytes.Equal(out, raw) || (len(out) == 0 && len(raw) > 0) {
		return ""
	}
	before := hashBytes(raw)
	err = atomicReplace(resolved, out, info.Mode().Perm(), func() error {
		if now, err := hashFile(resolved); err != nil || now != before {
			return errChanged
		}
		return nil
	})
	if err != nil {
		return ""
	}
	if seen, ok := fileVersions.seenHash(ctx.SessionID, resolved); ok && seen == before {
		fileVersions.record(ctx.SessionID, resolved, hashBytes(out))
	} else {
		fileVersions.forget(ctx.SessionID, resolved)
	}
	return name
}

func runFormatter(ctx *ToolCtx, argv []string, dir string, input []byte) ([]byte, error) {
	parent := context.Background()
	if ctx.Cancel != nil {
		parent = ctx.Cancel
	}
	cctx, cancel := context.WithTimeout(parent, formatTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = ChildEnv("NO_COLOR=1")
	if sandbox.Kind() != "" {
		if err := confine(cmd, &sandbox.Policy{Mode: sandbox.ModeReadOnly}); err != nil {
			return nil, err
		}
	}
	cmd.Stdin = bytes.NewReader(input)
	var stdout limitedBuffer
	stdout.max = 2 * MaxFileBytes
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	if stdout.over {
		return nil, errors.New("formatter output too large")
	}
	return stdout.Bytes(), nil
}

// limitedBuffer stops collecting past max bytes.
type limitedBuffer struct {
	bytes.Buffer
	max  int
	over bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.max {
		b.over = true
		return len(p), nil
	}
	return b.Buffer.Write(p)
}
