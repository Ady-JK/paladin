// recall.go holds the knowledge tools — skill (load a
// SKILL.md on demand) and session_search (recall past sessions).

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/Ady-JK/paladin/core/internal/jsonx"
	"github.com/Ady-JK/paladin/core/internal/sessionindex"
	"github.com/Ady-JK/paladin/core/internal/skills"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

// SkillTool lets the model load a skill from the catalog in its system
// prompt (progressive disclosure: names and descriptions up front,
// bodies only when relevant).
type SkillTool struct {
	dataDir    string
	supplement func(cwd, sessionID, name string) string
}

func NewSkillTool(dataDir string) *SkillTool { return &SkillTool{dataDir: dataDir} }

// WithSupplement adds runtime-specific context to selected skills.
func (t *SkillTool) WithSupplement(fn func(cwd, sessionID, name string) string) *SkillTool {
	t.supplement = fn
	return t
}

func (*SkillTool) Name() string       { return "skill" }
func (*SkillTool) ParallelSafe() bool { return true }

func (*SkillTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "skill",
		Description: "Load a skill's full instructions by name. The available skills are listed with " +
			"their descriptions in the system prompt; when a task matches one, load it before starting " +
			"and follow its instructions.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{"type": "string", "description": "Skill name exactly as listed."},
			},
			"required": []string{"name"},
		},
	}
}

func (t *SkillTool) Call(raw json.RawMessage, ctx *ToolCtx) (string, error) {
	var args struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &args); err != nil || strings.TrimSpace(args.Name) == "" {
		return "", ArgsError("skill: name is required")
	}
	cwd := ""
	if ctx != nil {
		cwd = ctx.Workspace
	}
	s, err := skills.Load(t.dataDir, cwd, args.Name)
	if err != nil {
		names := []string{}
		for _, e := range skills.List(t.dataDir, cwd) {
			names = append(names, e.Name)
		}
		return "", ArgsError(fmt.Sprintf("skill: %v; available: %s", err, strings.Join(names, ", ")))
	}
	if t.supplement != nil {
		sessionID := ""
		if ctx != nil {
			sessionID = ctx.SessionID
		}
		s.Content += t.supplement(cwd, sessionID, s.Name)
	}
	data, _ := jsonx.Marshal(map[string]any{"name": s.Name, "source": s.Source, "path": s.Path, "content": s.Content})
	return string(data), nil
}

// SkillCatalog renders the system-prompt list of skills, or "".
func SkillCatalog(dataDir, cwd string, max int) string {
	list := skills.List(dataDir, cwd)
	if len(list) == 0 {
		return ""
	}
	// Keep Creator guidance visible even when a large skill catalog is capped.
	slices.SortStableFunc(list, func(a, b skills.Skill) int {
		if skills.IsCreatorSkill(a.Name) && !skills.IsCreatorSkill(b.Name) {
			return -1
		}
		if !skills.IsCreatorSkill(a.Name) && skills.IsCreatorSkill(b.Name) {
			return 1
		}
		return 0
	})
	var b strings.Builder
	b.WriteString("Skills (load one with the skill tool when the task matches its description):\n")
	for i, s := range list {
		if i == max {
			fmt.Fprintf(&b, "- … and %d more\n", len(list)-max)
			break
		}
		desc := s.Description
		if desc == "" {
			desc = "(no description)"
		}
		fmt.Fprintf(&b, "- %s: %s\n", s.Name, desc)
	}
	return strings.TrimRight(b.String(), "\n")
}

// SessionSearchTool searches past session transcripts. It prefers the
// SQLite FTS index when one is attached (ranked, after a freshness scan)
// and falls back to substring matching over the transcripts when
// the index is missing or fails, so recall never disappears entirely.
type SessionSearchTool struct {
	store *transcript.Store
	index *sessionindex.Index
	// beforeSearch brings the index up to date on each search, so recall
	// never trusts a failed refresh.
	beforeSearch func(context.Context) error
}

func NewSessionSearchTool(store *transcript.Store) *SessionSearchTool {
	return &SessionSearchTool{store: store}
}

// WithIndex attaches the FTS index and the hook that keeps it current. A
// nil index keeps the substring path.
func (t *SessionSearchTool) WithIndex(idx *sessionindex.Index, beforeSearch func(context.Context) error) *SessionSearchTool {
	t.index = idx
	t.beforeSearch = beforeSearch
	return t
}

func (*SessionSearchTool) Name() string       { return "session_search" }
func (*SessionSearchTool) ParallelSafe() bool { return true }

func (*SessionSearchTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "session_search",
		Description: "Search the user's past Paladin sessions (other conversations, not this one) for " +
			"earlier decisions, fixes, commands, or context. Full-text: every query word must appear, " +
			"ranked by relevance, with a snippet from the best-matching message. Scope `folder` (default) " +
			"searches sessions of the current workspace; `all` searches every folder.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "Words to find, e.g. \"litellm proxy port\"."},
				"scope": map[string]any{"type": "string", "enum": []string{"folder", "all"}},
				"limit": map[string]any{"type": "integer", "description": "Max sessions to return (default 5, max 20)."},
			},
			"required": []string{"query"},
		},
	}
}

type searchSnippet struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

type searchHit struct {
	SessionID string          `json:"sessionId"`
	Title     string          `json:"title"`
	Cwd       string          `json:"cwd"`
	Created   string          `json:"created"`
	Score     int             `json:"score"`
	Snippets  []searchSnippet `json:"snippets"`
}

const (
	searchSnippetLen = 160
)

func (t *SessionSearchTool) Call(raw json.RawMessage, ctx *ToolCtx) (string, error) {
	var args struct {
		Query string `json:"query"`
		Scope string `json:"scope"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", ArgsError("session_search: the arguments are not valid JSON")
	}
	terms := strings.Fields(strings.ToLower(args.Query))
	if len(terms) == 0 {
		return "", ArgsError("session_search: query is required")
	}
	limit := args.Limit
	if limit <= 0 {
		limit = 5
	}
	if limit > 20 {
		limit = 20
	}
	cwd, self := "", ""
	if ctx != nil {
		self = ctx.SessionID
		if args.Scope != "all" {
			cwd = ctx.Workspace
		}
	}
	if hits, err := t.searchIndex(ctx, args.Query, cwd, self, limit); err == nil && hits != nil {
		data, _ := jsonx.Marshal(map[string]any{"query": args.Query, "matches": len(hits), "results": hits, "source": "fts"})
		return string(data), nil
	}
	infos, err := t.store.List(cwd, 0)
	if err != nil {
		return "", ArgsError("session_search: " + err.Error())
	}
	var hits []searchHit
	for _, info := range infos {
		if ctx != nil && ctx.Cancel != nil && ctx.Cancel.Err() != nil {
			return "", ArgsError("session_search: interrupted")
		}
		if info.Meta.SessionID == self {
			continue
		}
		sess, err := t.store.Load(info.Meta.Cwd, info.Meta.SessionID)
		if err != nil {
			continue
		}
		msgs, err := sess.History()
		if err != nil {
			continue
		}
		if hit, ok := matchSession(msgs, terms); ok {
			hit.SessionID, hit.Title, hit.Cwd = info.Meta.SessionID, info.Title, info.Meta.Cwd
			if hit.Title == "" {
				hit.Title = "untitled session"
			}
			hit.Created = info.Meta.CreatedAt.Format(time.RFC3339)
			hits = append(hits, hit)
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	total := len(hits)
	if len(hits) > limit {
		hits = hits[:limit]
	}
	if hits == nil {
		hits = []searchHit{}
	}
	data, _ := jsonx.Marshal(map[string]any{"query": args.Query, "matches": total, "results": hits, "searched": len(infos), "source": "scan"})
	return string(data), nil
}

// searchIndex answers from the FTS index. It returns (nil, nil) when there
// is no index, which sends the caller down the substring path; a real
// error is also a fallback, never a failed search.
func (t *SessionSearchTool) searchIndex(ctx *ToolCtx, query, cwd, self string, limit int) ([]searchHit, error) {
	if t.index == nil {
		return nil, nil
	}
	ctxv := context.Background()
	if ctx != nil && ctx.Cancel != nil {
		ctxv = ctx.Cancel
	}
	if t.beforeSearch != nil {
		if err := t.beforeSearch(ctxv); err != nil {
			return nil, err
		}
	}
	hits, err := t.index.Search(ctxv, query, cwd, self, limit)
	if err != nil {
		return nil, err
	}
	out := make([]searchHit, 0, len(hits))
	for _, h := range hits {
		out = append(out, searchHit{
			SessionID: h.SessionID, Title: h.Title, Cwd: h.Cwd, Created: h.Created,
			Score: h.Score, Snippets: []searchSnippet{{Role: h.Role, Text: h.Snippet}},
		})
	}
	return out, nil
}

// matchSession requires every term somewhere in the session's user and
// assistant text; the score counts term occurrences.
func matchSession(msgs []transcript.Message, terms []string) (searchHit, bool) {
	type scored struct {
		role, text string
		n          int
	}
	found := map[string]bool{}
	var cands []scored
	score := 0
	for _, m := range msgs {
		if m.Replay || (m.Role != transcript.RoleUser && m.Role != transcript.RoleAssistant) {
			continue
		}
		lower := strings.ToLower(m.Content)
		n := 0
		for _, term := range terms {
			if c := strings.Count(lower, term); c > 0 {
				found[term] = true
				n += c
			}
		}
		if n > 0 {
			score += n
			cands = append(cands, scored{string(m.Role), m.Content, n})
		}
	}
	if len(found) < len(terms) {
		return searchHit{}, false
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].n > cands[j].n })
	hit := searchHit{Score: score}
	for i, c := range cands {
		if i == 3 {
			break
		}
		hit.Snippets = append(hit.Snippets, searchSnippet{Role: c.role, Text: snippet(c.text, terms)})
	}
	return hit, true
}

func snippet(text string, terms []string) string {
	lower := strings.ToLower(text)
	at := -1
	for _, term := range terms {
		if i := strings.Index(lower, term); i >= 0 && (at < 0 || i < at) {
			at = i
		}
	}
	runes := []rune(text)
	// Map the byte offset to a rune offset.
	pos := len([]rune(text[:max(at, 0)]))
	start := max(pos-searchSnippetLen/2, 0)
	end := min(start+searchSnippetLen, len(runes))
	out := strings.Join(strings.Fields(string(runes[start:end])), " ")
	if start > 0 {
		out = "…" + out
	}
	if end < len(runes) {
		out += "…"
	}
	return out
}
