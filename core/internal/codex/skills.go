package codex

import "context"

// Skill is one skill Codex can use in a folder: a user's, the repo's, or one
// that ships with Codex.
type Skill struct {
	Name        string
	Title       string // its display name, when it has one
	Description string
	Scope       string // user, repo, system or admin
	Path        string // its SKILL.md
	Enabled     bool
}

// ListSkills lists the skills available for work in cwd.
func (c *Client) ListSkills(ctx context.Context, cwd string) ([]Skill, error) {
	var res struct {
		Data []Raw `json:"data"`
	}
	if err := c.Call(ctx, "skills/list", map[string]any{"cwds": []string{cwd}}, &res); err != nil {
		return nil, err
	}
	var out []Skill
	seen := map[string]bool{}
	for _, entry := range res.Data {
		for _, s := range entry.List("skills") {
			name := s.Str("name")
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			iface := s.Obj("interface")
			desc := s.Str("shortDescription")
			if desc == "" {
				desc = iface.Str("shortDescription")
			}
			if full := s.Str("description"); full != "" {
				desc = full
			}
			out = append(out, Skill{Name: name, Title: iface.Str("displayName"), Description: desc, Scope: s.Str("scope"),
				Path: s.Str("path"), Enabled: !s.Has("enabled") || boolOf(s["enabled"])})
		}
	}
	return out, nil
}

// McpServer is one MCP server Codex knows of, and how it is doing.
type McpServer struct {
	Name       string
	Status     string // connected, starting, failed, authenticationRequired, disabled, … or "" when not started
	Auth       string // the login state: oAuth, bearerToken, notLoggedIn, unsupported, …
	Tools      int
	ToolsError string
}

// McpServers lists the MCP servers Codex has for a thread and their state,
// without asking each for its full tool descriptions.
func (c *Client) McpServers(ctx context.Context, threadID string) ([]McpServer, error) {
	var out []McpServer
	cursor := ""
	for page := 0; page < 20; page++ {
		params := map[string]any{"threadId": threadID, "detail": "toolsAndAuthOnly"}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var res struct {
			Data       []Raw   `json:"data"`
			NextCursor *string `json:"nextCursor"`
		}
		if err := c.Call(ctx, "mcpServerStatus/list", params, &res); err != nil {
			return nil, err
		}
		for _, s := range res.Data {
			out = append(out, McpServer{Name: s.Str("name"), Status: s.Str("runtimeStatus"), Auth: s.Str("authStatus"),
				Tools: len(Decode(s["tools"])), ToolsError: s.Str("toolsError")})
		}
		if res.NextCursor == nil || *res.NextCursor == "" {
			break
		}
		cursor = *res.NextCursor
	}
	return out, nil
}
