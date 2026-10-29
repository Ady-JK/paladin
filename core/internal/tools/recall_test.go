package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/transcript"
)

func TestSkillToolLoadsAndListsOnMiss(t *testing.T) {
	data := t.TempDir()
	dir := filepath.Join(data, "config", "skills", "deploy")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\ndescription: ship it\n---\nsteps"), 0o644)
	tool := NewSkillTool(data)
	out, err := tool.Call(json.RawMessage(`{"name":"deploy"}`), &ToolCtx{})
	if err != nil || !strings.Contains(out, "steps") {
		t.Fatalf("out=%s err=%v", out, err)
	}
	_, err = tool.Call(json.RawMessage(`{"name":"nope"}`), &ToolCtx{})
	if err == nil || !strings.Contains(err.(*ToolError).ResultContent(), "deploy") {
		t.Fatalf("miss should list available: %v", err)
	}
	cat := SkillCatalog(data, "", 10)
	if !strings.Contains(cat, "- deploy: ship it") {
		t.Fatalf("catalog = %q", cat)
	}
	if SkillCatalog(t.TempDir(), "", 10) != "" {
		t.Fatal("empty catalog must be blank")
	}
}

func TestSessionSearch(t *testing.T) {
	store := transcript.NewStore(t.TempDir())
	mk := func(cwd string, msgs ...string) transcript.Session {
		s, err := store.Create(cwd, "", "")
		if err != nil {
			t.Fatal(err)
		}
		for i, m := range msgs {
			role := transcript.RoleUser
			if i%2 == 1 {
				role = transcript.RoleAssistant
			}
			_ = s.Append(role, m)
		}
		return s
	}
	a := mk("/w", "how do I start the litellm proxy", "run litellm on port 4000")
	_ = mk("/w", "unrelated chat about css")
	_ = mk("/other", "litellm proxy elsewhere port")
	self := mk("/w", "litellm proxy port in this session")

	tool := NewSessionSearchTool(store)
	out, err := tool.Call(json.RawMessage(`{"query":"LiteLLM port"}`), &ToolCtx{Workspace: "/w", SessionID: self.Meta.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		Matches int
		Results []searchHit
	}
	_ = json.Unmarshal([]byte(out), &res)
	if res.Matches != 1 || res.Results[0].SessionID != a.Meta.SessionID || len(res.Results[0].Snippets) == 0 {
		t.Fatalf("folder search = %s", out)
	}
	out, _ = tool.Call(json.RawMessage(`{"query":"litellm port","scope":"all"}`), &ToolCtx{Workspace: "/w", SessionID: self.Meta.SessionID})
	_ = json.Unmarshal([]byte(out), &res)
	if res.Matches != 2 {
		t.Fatalf("all search = %s", out)
	}
	if _, err := tool.Call(json.RawMessage(`{"query":"  "}`), &ToolCtx{}); err == nil {
		t.Fatal("blank query must fail")
	}
}

func TestSnippetUnicodeSafe(t *testing.T) {
	text := strings.Repeat("é", 300) + " needle " + strings.Repeat("ü", 300)
	s := snippet(text, []string{"needle"})
	if !strings.Contains(s, "needle") || !strings.HasPrefix(s, "…") {
		t.Fatalf("snippet = %q", s)
	}
}
