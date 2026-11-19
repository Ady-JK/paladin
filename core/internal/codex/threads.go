package codex

import (
	"context"
	"encoding/json"
	"fmt"
)

// ThreadSummary is one thread Codex has on disk, started by any client.
type ThreadSummary struct {
	ID        string
	Preview   string // the first thing the user said
	Name      string // a name the user gave it, when there is one
	Cwd       string
	Model     string
	UpdatedAt int64 // unix seconds
	CreatedAt int64 // unix seconds
	Source    string
}

// ThreadQuery narrows a thread listing.
type ThreadQuery struct {
	Cwd    string // only threads that ran in this folder
	Search string // a substring of the title
	Cursor string
	Limit  int
}

// ListThreads lists Codex's saved threads, most recently updated first, with
// the cursor of the next page (empty at the end).
func (c *Client) ListThreads(ctx context.Context, q ThreadQuery) ([]ThreadSummary, string, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 30
	}
	params := map[string]any{"limit": limit, "sortKey": "updated_at", "sortDirection": "desc", "archived": false}
	if q.Cwd != "" {
		params["cwd"] = q.Cwd
	}
	if q.Search != "" {
		params["searchTerm"] = q.Search
	}
	if q.Cursor != "" {
		params["cursor"] = q.Cursor
	}
	var res struct {
		Data       []Raw   `json:"data"`
		NextCursor *string `json:"nextCursor"`
	}
	if err := c.Call(ctx, "thread/list", params, &res); err != nil {
		return nil, "", err
	}
	out := make([]ThreadSummary, 0, len(res.Data))
	for _, t := range res.Data {
		out = append(out, ThreadSummary{ID: t.Str("id"), Preview: t.Str("preview"), Name: t.Str("name"), Cwd: t.Str("cwd"),
			Model: t.Str("model"), UpdatedAt: t.Int("updatedAt"), CreatedAt: t.Int("createdAt"), Source: sourceName(t["source"])})
	}
	next := ""
	if res.NextCursor != nil {
		next = *res.NextCursor
	}
	return out, next, nil
}

// sourceName reads a thread's source, which is a plain name for the common
// clients and an object for a subagent's.
func sourceName(raw json.RawMessage) string {
	var name string
	if json.Unmarshal(raw, &name) == nil {
		return name
	}
	return "other"
}

// maxThreadTurnPages bounds how many pages of a thread's history are read.
const maxThreadTurnPages = 200

// LatestTurn is a cheap change probe for a linked conversation. A full item view
// detects streamed edits as well as completed turns, even within one second.
func (c *Client) LatestTurn(ctx context.Context, threadID string) (Raw, error) {
	var res struct {
		Data []Raw `json:"data"`
	}
	if err := c.Call(ctx, "thread/turns/list", map[string]any{"threadId": threadID, "sortDirection": "desc", "itemsView": "full", "limit": 1}, &res); err != nil {
		return nil, err
	}
	if len(res.Data) == 0 {
		return nil, nil
	}
	return res.Data[0], nil
}

// ThreadTurns reads a thread's whole history, oldest turn first, with every
// item in full.
func (c *Client) ThreadTurns(ctx context.Context, threadID string) ([]Raw, error) {
	var turns []Raw
	cursor, seen := "", map[string]bool{}
	for page := 0; page < maxThreadTurnPages; page++ {
		params := map[string]any{"threadId": threadID, "sortDirection": "asc", "itemsView": "full", "limit": 50}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var res struct {
			Data       []Raw   `json:"data"`
			NextCursor *string `json:"nextCursor"`
		}
		if err := c.Call(ctx, "thread/turns/list", params, &res); err != nil {
			return nil, err
		}
		turns = append(turns, res.Data...)
		if res.NextCursor == nil || *res.NextCursor == "" {
			return turns, nil
		}
		if seen[*res.NextCursor] {
			return nil, fmt.Errorf("codex thread history paging repeated a cursor")
		}
		seen[*res.NextCursor] = true
		cursor = *res.NextCursor
	}
	return nil, fmt.Errorf("codex thread %s has more history than Paladin will import", threadID)
}

// ReadThread reads one saved thread's details, without its turns.
func (c *Client) ReadThread(ctx context.Context, threadID string) (ThreadSummary, error) {
	var res struct {
		Thread Raw `json:"thread"`
	}
	if err := c.Call(ctx, "thread/read", map[string]any{"threadId": threadID, "includeTurns": false}, &res); err != nil {
		return ThreadSummary{}, err
	}
	t := res.Thread
	if t.Str("id") == "" {
		return ThreadSummary{}, fmt.Errorf("codex has no thread %s", threadID)
	}
	return ThreadSummary{ID: t.Str("id"), Preview: t.Str("preview"), Name: t.Str("name"), Cwd: t.Str("cwd"),
		Model: t.Str("model"), UpdatedAt: t.Int("updatedAt"), CreatedAt: t.Int("createdAt"), Source: sourceName(t["source"])}, nil
}
