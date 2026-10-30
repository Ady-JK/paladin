package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestReadOnlyCommand(t *testing.T) {
	ok := []string{
		"ls -la",
		"cat go.mod | head -20",
		"rg -n foo src && wc -l a.go",
		"git status; git diff HEAD~1 -- a.go",
		"git log --oneline -5",
		"find . -name '*.go' 2>/dev/null",
		"grep -r 'a > b' .",
		"cd sub && ls",
		"FOO=1 ls",
		"go list ./...",
		"sed -n 1,20p a.go",
	}
	for _, c := range ok {
		if err := ReadOnlyCommand(c); err != nil {
			t.Errorf("%q rejected: %v", c, err)
		}
	}
	bad := []string{
		"rm -rf x",
		"echo hi > file",
		"cat a >> b",
		"ls && touch x",
		"git commit -m x",
		"git push",
		"sed -i '' s/a/b/ f",
		"find . -delete",
		"find . -exec rm {} ;",
		"npm install",
		"$(rm x)",
		"ls `rm x`",
		"python3 -c 'print(1)'",
		"tee out",
		"",
		// Environment and config dumps can expose secrets.
		"env",
		"FOO=1 env",
		"printenv",
		"printenv OPENAI_API_KEY",
		"go env",
		"git config --list",
		"git config --get remote.origin.url",
		"awk 'BEGIN{print ENVIRON[\"HOME\"]}'",
		"jq -n env",
		"jq -n '$ENV.PATH'",
	}
	for _, c := range bad {
		if err := ReadOnlyCommand(c); err == nil {
			t.Errorf("%q accepted", c)
		}
	}
}

func TestPlanShellBlocksWrites(t *testing.T) {
	inner := NewShellTool()
	p := NewPlanShell(inner)
	if p.Name() != "shell" || !strings.Contains(p.Definition().Description, "read-only") {
		t.Fatalf("definition = %+v", p.Definition())
	}
	_, err := p.Call(json.RawMessage(`{"cmd":"rm -rf /tmp/x"}`), &ToolCtx{Workspace: t.TempDir(), Cancel: context.Background()})
	if err == nil || !strings.Contains(err.Error(), "plan mode") {
		t.Fatalf("err = %v", err)
	}
	out, err := p.Call(json.RawMessage(`{"cmd":"echo-not-allowed"}`), &ToolCtx{Workspace: t.TempDir(), Cancel: context.Background()})
	if err == nil {
		t.Fatalf("unknown program accepted: %s", out)
	}
	out, err = p.Call(json.RawMessage(`{"cmd":"pwd"}`), &ToolCtx{Workspace: t.TempDir(), Cancel: context.Background()})
	if err != nil || !strings.Contains(out, "completed") && !strings.Contains(out, "exit_code") {
		t.Fatalf("pwd: out=%s err=%v", out, err)
	}
}
