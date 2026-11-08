package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/sessionindex"
	"github.com/Ady-JK/paladin/core/internal/skills"
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
	if !strings.Contains(SkillCatalog(t.TempDir(), "", 10), "- debugging:") {
		t.Fatal("built-in skills are in the catalog")
	}
	// With every built-in turned off and no skills of its own, the
	// catalog is blank.
	empty := t.TempDir()
	var names []string
	for _, s := range skills.Builtins() {
		names = append(names, `"`+s.Name+`"`)
	}
	os.MkdirAll(filepath.Join(empty, "config"), 0o755)
	os.WriteFile(filepath.Join(empty, "config", "settings.json"), []byte(`{"disabledSkills":[`+strings.Join(names, ",")+`]}`), 0o644)
	if SkillCatalog(empty, "", 10) != "" {
		t.Fatal("empty catalog must be blank")
	}
}

func TestSessionSearchFallsBackWhenRefreshFails(t *testing.T) {
	data := t.TempDir()
	store := transcript.NewStore(filepath.Join(data, "sessions"))
	sess, err := store.Create("/w", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.Append(transcript.RoleUser, "newlysavedword"); err != nil {
		t.Fatal(err)
	}
	idx, err := sessionindex.Open(sessionindex.Path(data))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()
	attempts := 0
	tool := NewSessionSearchTool(store).WithIndex(idx, func(ctx context.Context) error {
		attempts++
		if attempts == 1 {
			return errors.New("refresh unavailable")
		}
		_, err := idx.Refresh(ctx, sessionindex.Source{Store: store, DataDir: data})
		return err
	})
	for _, source := range []string{"scan", "fts"} {
		out, err := tool.Call(json.RawMessage(`{"query":"newlysavedword"}`), &ToolCtx{Workspace: "/w"})
		if err != nil || !strings.Contains(out, sess.Meta.SessionID) || !strings.Contains(out, `"source":"`+source+`"`) {
			t.Fatalf("fallback: %s %v", out, err)
		}
	}
	if attempts != 2 {
		t.Fatalf("refresh attempts = %d", attempts)
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

// TestSessionSearchPrefersTheIndex checks the FTS path answers from the
// index, and that a missing index still finds the same session by
// scanning — the fallback that keeps recall from disappearing.
func TestSessionSearchPrefersTheIndex(t *testing.T) {
	data := t.TempDir()
	store := transcript.NewStore(filepath.Join(data, "sessions"))
	sess, err := store.Create("/w", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.Append(transcript.RoleUser, "we run the litellm proxy on port 4000"); err != nil {
		t.Fatal(err)
	}
	idx, err := sessionindex.Open(sessionindex.Path(data))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()
	refreshed := 0
	tool := NewSessionSearchTool(store).WithIndex(idx, func(context.Context) error {
		refreshed++
		if _, err := idx.Refresh(context.Background(), sessionindex.Source{Store: store, DataDir: data}); err != nil {
			t.Errorf("refresh: %v", err)
		}
		return nil
	})
	out, err := tool.Call(json.RawMessage(`{"query":"litellm port"}`), &ToolCtx{Workspace: "/w"})
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		Source  string
		Results []searchHit
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if res.Source != "fts" || len(res.Results) != 1 || res.Results[0].SessionID != sess.Meta.SessionID {
		t.Fatalf("index path = %s", out)
	}
	if refreshed != 1 {
		t.Fatalf("index refreshed %d times, want 1 per search", refreshed)
	}

	// Without an index, the same query still finds the session.
	plain := NewSessionSearchTool(store)
	out, err = plain.Call(json.RawMessage(`{"query":"litellm port"}`), &ToolCtx{Workspace: "/w"})
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal([]byte(out), &res)
	if res.Source != "scan" || len(res.Results) != 1 {
		t.Fatalf("fallback path = %s", out)
	}
}
