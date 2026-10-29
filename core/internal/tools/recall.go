// recall.go holds the knowledge tools — skill (load a
// SKILL.md on demand) and session_search (recall past sessions).

package tools

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Ady-JK/paladin/core/internal/jsonx"
	"github.com/Ady-JK/paladin/core/internal/skills"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

// SkillTool lets the model load a skill from the catalog in its system
// prompt (progressive disclosure: names and descriptions up front,
// bodies only when relevant).
type SkillTool struct{ dataDir string }

func NewSkillTool(dataDir string) *SkillTool { return &SkillTool{dataDir: dataDir} }

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
	data, _ := jsonx.Marshal(map[string]any{"name": s.Name, "source": s.Source, "path": s.Path, "content": s.Content})
	return string(data), nil
}

// SkillCatalog renders the system-prompt list of skills, or "".
func SkillCatalog(dataDir, cwd string, max int) string {
	list := skills.List(dataDir, cwd)
	if len(list) == 0 {
		return ""
	}
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

// SessionSearchTool searches past session transcripts.
type SessionSearchTool struct{ store *transcript.Store }

func NewSessionSearchTool(store *transcript.Store) *SessionSearchTool {
	return &SessionSearchTool{store: store}
}

func (*SessionSearchTool) Name() string       { return "session_search" }
func (*SessionSearchTool) ParallelSafe() bool { return true }

func (*SessionSearchTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "session_search",
		Description: "Search the user's past Paladin sessions (other conversations, not this one) for " +
			"earlier decisions, fixes, commands, or context. Matches sessions containing every query " +
			"word (case-insensitive) and returns titles with matching snippets. Scope `folder` (default) " +
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
	searchMaxSessions = 400
	searchSnippetLen  = 160
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
	infos, err := t.store.List(cwd, searchMaxSessions)
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
	data, _ := jsonx.Marshal(map[string]any{"query": args.Query, "matches": total, "results": hits, "searched": len(infos)})
	return string(data), nil
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
