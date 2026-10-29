package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestListMergesBuiltinGlobalWorkspace(t *testing.T) {
	data, cwd := t.TempDir(), t.TempDir()
	write(t, filepath.Join(data, "config", "commands", "deploy.md"), "---\ndescription: Deploy it\nargument-hint: <env>\n---\nDeploy to $1 now. All: $ARGUMENTS")
	write(t, filepath.Join(cwd, ".paladin", "commands", "deploy.md"), "---\ndescription: Workspace deploy\n---\nws deploy $ARGUMENTS")
	write(t, filepath.Join(cwd, ".paladin", "commands", "fix.md"), "Fix the bug")
	write(t, filepath.Join(cwd, ".paladin", "commands", "bad name.md"), "x")

	list := List(data, cwd)
	byName := map[string]Command{}
	for _, c := range list {
		byName[c.Name] = c
	}
	if byName["deploy"].Source != SourceWorkspace || byName["deploy"].Description != "Workspace deploy" {
		t.Fatalf("workspace should shadow global: %+v", byName["deploy"])
	}
	if byName["fix"].Description != "Fix the bug" {
		t.Fatalf("description fallback: %+v", byName["fix"])
	}
	if byName["init"].Source != SourceBuiltin || byName["review"].Source != SourceBuiltin {
		t.Fatal("builtins missing")
	}
	if byName["review"].Mode != "plan" {
		t.Fatal("review runs in plan mode")
	}
	if _, ok := byName["bad name"]; ok {
		t.Fatal("invalid names must be skipped")
	}
}

func TestExpand(t *testing.T) {
	cases := []struct{ body, args, want string }{
		{"Deploy to $1 ($2). All: $ARGUMENTS", "prod eu-west", "Deploy to prod (eu-west). All: prod eu-west"},
		{"Fix it", "the login bug", "Fix it\n\nthe login bug"},
		{"Fix it", "", "Fix it"},
		{"Use $1 and $3", `"a b" c`, "Use a b and "},
	}
	for _, c := range cases {
		if got := Expand(c.body, c.args); got != c.want {
			t.Errorf("Expand(%q,%q) = %q want %q", c.body, c.args, got, c.want)
		}
	}
}

func TestLoadAndBodyStripsFrontmatter(t *testing.T) {
	data, cwd := t.TempDir(), t.TempDir()
	write(t, filepath.Join(cwd, ".paladin", "commands", "hi.md"), "---\ndescription: d\n---\nHello $ARGUMENTS")
	c, err := Load(data, cwd, "HI")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(c.Body, "description") || !strings.HasPrefix(c.Body, "Hello") {
		t.Fatalf("body = %q", c.Body)
	}
	if _, err := Load(data, cwd, "../etc"); err == nil {
		t.Fatal("path names must fail")
	}
	if _, err := Load(data, cwd, "missing"); err == nil {
		t.Fatal("missing must fail")
	}
	b, err := Load(data, cwd, "init")
	if err != nil || !strings.Contains(b.Body, "AGENTS.md") {
		t.Fatalf("builtin init: %v", err)
	}
}
