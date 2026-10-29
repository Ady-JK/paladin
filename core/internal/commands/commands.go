// Package commands loads custom prompt commands: markdown files whose
// body becomes the user message when invoked as `/name args`.
//
//	config/commands/<name>.md              (global, under the data dir)
//	<workspace>/.paladin/commands/<name>.md  (workspace, shadows global)
//
// Frontmatter keys: description, argument-hint, mode (plan|build).
// The body may reference $ARGUMENTS (the whole argument string) and
// $1..$9 (whitespace-split, quotes group). When the body names neither,
// the arguments are appended after a blank line. Built-in commands
// (init, review) ship in the binary and can be shadowed by files.
package commands

import (
	"embed"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/Ady-JK/paladin/core/internal/skills"
)

const (
	SourceBuiltin   = "builtin"
	SourceGlobal    = "global"
	SourceWorkspace = "workspace"

	MaxBytes = 64 * 1024
)

//go:embed builtin/*.md
var builtinFS embed.FS

// Command is one invocable prompt command.
type Command struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	ArgumentHint string `json:"argumentHint,omitempty"`
	Mode         string `json:"mode,omitempty"`
	Source       string `json:"source"`
	Path         string `json:"path,omitempty"`
	Body         string `json:"-"`
}

var validName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

// ValidName reports whether name can be a command.
func ValidName(name string) bool { return validName.MatchString(name) }

func GlobalDir(dataDir string) string { return filepath.Join(dataDir, "config", "commands") }

func WorkspaceDir(cwd string) string {
	if strings.TrimSpace(cwd) == "" {
		return ""
	}
	return filepath.Join(cwd, ".paladin", "commands")
}

// List returns every command, workspace over global over builtin, by name.
func List(dataDir, cwd string) []Command {
	byName := map[string]Command{}
	for _, c := range builtins() {
		byName[strings.ToLower(c.Name)] = c
	}
	for _, root := range []struct{ dir, source string }{{GlobalDir(dataDir), SourceGlobal}, {WorkspaceDir(cwd), SourceWorkspace}} {
		for _, c := range scan(root.dir, root.source) {
			byName[strings.ToLower(c.Name)] = c
		}
	}
	out := make([]Command, 0, len(byName))
	for _, c := range byName {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Load finds one command by name (case-insensitive) with its body.
func Load(dataDir, cwd, name string) (Command, error) {
	name = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(name), "/"))
	if !ValidName(name) {
		return Command{}, fmt.Errorf("parse: invalid command name %q", name)
	}
	for _, c := range List(dataDir, cwd) {
		if strings.EqualFold(c.Name, name) {
			return c, nil
		}
	}
	return Command{}, fmt.Errorf("not found: command /%s (add %s.md under .paladin/commands/ or config/commands/)", name, name)
}

func builtins() []Command {
	entries, _ := builtinFS.ReadDir("builtin")
	var out []Command
	for _, e := range entries {
		data, err := builtinFS.ReadFile("builtin/" + e.Name())
		if err != nil {
			continue
		}
		c := parse(strings.TrimSuffix(e.Name(), ".md"), string(data))
		c.Source = SourceBuiltin
		out = append(out, c)
	}
	return out
}

func scan(dir, source string) []Command {
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Command
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".md") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		if !ValidName(name) {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := readCapped(path)
		if err != nil || len(data) > MaxBytes {
			continue
		}
		c := parse(name, string(data))
		c.Source, c.Path = source, path
		out = append(out, c)
	}
	return out
}

func readCapped(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, MaxBytes+1))
}

func parse(name, content string) Command {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	c := Command{Name: name, Body: strings.TrimSpace(content)}
	if fm, rest, ok := skills.SplitFrontmatter(content); ok {
		c.Body = strings.TrimSpace(rest)
		c.Description = skills.FrontmatterValue(fm, "description")
		c.ArgumentHint = skills.FrontmatterValue(fm, "argument-hint")
		if mode := strings.ToLower(skills.FrontmatterValue(fm, "mode")); mode == "plan" || mode == "build" {
			c.Mode = mode
		}
	}
	if c.Description == "" {
		for _, line := range strings.Split(c.Body, "\n") {
			line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#"))
			if line != "" {
				c.Description = clip(line, 120)
				break
			}
		}
	}
	return c
}

var posArg = regexp.MustCompile(`\$([1-9])`)

// Expand substitutes the arguments into body.
func Expand(body, args string) string {
	args = strings.TrimSpace(args)
	usesArgs := strings.Contains(body, "$ARGUMENTS") || posArg.MatchString(body)
	if !usesArgs {
		if args == "" {
			return body
		}
		return body + "\n\n" + args
	}
	fields := splitArgs(args)
	out := strings.ReplaceAll(body, "$ARGUMENTS", args)
	return posArg.ReplaceAllStringFunc(out, func(m string) string {
		i := int(m[1] - '1')
		if i < len(fields) {
			return fields[i]
		}
		return ""
	})
}

// splitArgs splits on whitespace; double or single quotes group words.
func splitArgs(s string) []string {
	var out []string
	var cur strings.Builder
	var quote rune
	inField := false
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote, inField = r, true
		case r == ' ' || r == '\t' || r == '\n':
			if inField {
				out = append(out, cur.String())
				cur.Reset()
				inField = false
			}
		default:
			cur.WriteRune(r)
			inField = true
		}
	}
	if inField {
		out = append(out, cur.String())
	}
	return out
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
