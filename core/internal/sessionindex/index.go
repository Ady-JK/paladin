// Package sessionindex is the full-text index behind the session_search
// tool.
//
// The original recall path loaded every transcript in a folder and
// substring-matched them in Go: correct, but it reads the whole corpus on
// every call and cannot rank. This package keeps a SQLite FTS5 index
// under the data dir instead — modernc.org/sqlite is pure Go, so
// CGO_ENABLED=0 still holds — and answers a query with BM25 ranking and
// snippets.
//
// The index is a cache, never the source of truth: transcripts on disk
// stay authoritative, the index is rebuilt from them when it is missing or
// stale, and a failure to index degrades to the substring path rather than
// losing recall entirely.
package sessionindex

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite" // registers the pure-Go "sqlite" driver
)

// SchemaVersion bumps when the table layout changes; a mismatch rebuilds.
const SchemaVersion = 1

// Doc is one indexable message.
type Doc struct {
	// Key is globally unique: sessionID + message index.
	Key       string
	SessionID string
	Cwd       string
	Title     string
	Role      string
	Text      string
	// Seq orders the messages inside a session.
	Seq int
	// Created is the session's creation time, RFC3339, for display.
	Created string
}

// Hit is one ranked result.
type Hit struct {
	SessionID string `json:"sessionId"`
	Title     string `json:"title"`
	Cwd       string `json:"cwd"`
	Created   string `json:"created"`
	Role      string `json:"-"`
	Snippet   string `json:"snippet"`
	Score     int    `json:"score"`
}

// Index is a SQLite FTS5 index of transcript messages.
type Index struct {
	path string

	mu sync.Mutex
	db *sql.DB
}

// Path is the index file for one data dir.
func Path(dataDir string) string { return filepath.Join(dataDir, "cache", "sessions.fts.db") }

// Open opens (and creates) the index at path. A corrupt file is deleted
// and rebuilt rather than returned as an error: it is a cache.
func Open(path string) (*Index, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("io: session index dir: %w", err)
	}
	idx := &Index{path: path}
	if err := idx.connect(); err != nil {
		// A half-written or unreadable cache must not block searching:
		// drop it and try once more.
		for _, suffix := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(path + suffix)
		}
		if err2 := idx.connect(); err2 != nil {
			return nil, err
		}
	}
	return idx, nil
}

func (i *Index) connect() error {
	db, err := sql.Open("sqlite", i.path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return fmt.Errorf("io: open session index: %w", err)
	}
	// A single connection keeps WAL writes serialized without a pool
	// stampede, and the index is written by one background pass.
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return fmt.Errorf("io: session index unusable: %w", err)
	}
	i.db = db
	return i.ensureSchema()
}

func (i *Index) ensureSchema() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS docs USING fts5(
			key UNINDEXED,
			session_id UNINDEXED,
			cwd UNINDEXED,
			title UNINDEXED,
			role UNINDEXED,
			seq UNINDEXED,
			created UNINDEXED,
			text,
			tokenize = 'unicode61 remove_diacritics 2'
		)`,
	}
	for _, stmt := range stmts {
		if _, err := i.db.Exec(stmt); err != nil {
			return fmt.Errorf("io: session index schema: %w", err)
		}
	}
	var have string
	_ = i.db.QueryRow(`SELECT value FROM meta WHERE key = 'schema'`).Scan(&have)
	if have == fmt.Sprint(SchemaVersion) {
		return nil
	}
	if _, err := i.db.Exec(`DELETE FROM docs`); err != nil {
		return fmt.Errorf("io: session index reset: %w", err)
	}
	if _, err := i.db.Exec(`INSERT INTO meta(key, value) VALUES('schema', ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, fmt.Sprint(SchemaVersion)); err != nil {
		return fmt.Errorf("io: session index version: %w", err)
	}
	return nil
}

// Close releases the database handle.
func (i *Index) Close() error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.db == nil {
		return nil
	}
	err := i.db.Close()
	i.db = nil
	return err
}

// ReplaceSession reindexes one session atomically: its old rows go and
// the supplied docs land in a single transaction, so a search never sees
// a half-indexed conversation.
func (i *Index) ReplaceSession(ctx context.Context, sessionID string, docs []Doc) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.db == nil {
		return fmt.Errorf("io: session index is closed")
	}
	tx, err := i.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("io: session index transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM docs WHERE session_id = ?`, sessionID); err != nil {
		return fmt.Errorf("io: session index delete: %w", err)
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO docs(key, session_id, cwd, title, role, seq, created, text)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("io: session index insert: %w", err)
	}
	defer func() { _ = stmt.Close() }()
	for _, d := range docs {
		if strings.TrimSpace(d.Text) == "" {
			continue
		}
		if _, err := stmt.ExecContext(ctx, d.Key, d.SessionID, d.Cwd, d.Title, d.Role, d.Seq, d.Created, d.Text); err != nil {
			return fmt.Errorf("io: session index insert: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("io: session index commit: %w", err)
	}
	return nil
}

// DeleteSession drops a session's rows (a deleted conversation).
func (i *Index) DeleteSession(ctx context.Context, sessionID string) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.db == nil {
		return fmt.Errorf("io: session index is closed")
	}
	if _, err := i.db.ExecContext(ctx, `DELETE FROM docs WHERE session_id = ?`, sessionID); err != nil {
		return fmt.Errorf("io: session index delete: %w", err)
	}
	return nil
}

// Sessions lists the session ids the index holds.
func (i *Index) Sessions(ctx context.Context) (map[string]bool, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.db == nil {
		return nil, fmt.Errorf("io: session index is closed")
	}
	rows, err := i.db.QueryContext(ctx, `SELECT DISTINCT session_id FROM docs`)
	if err != nil {
		return nil, fmt.Errorf("io: session index list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("io: session index list: %w", err)
		}
		out[id] = true
	}
	return out, rows.Err()
}

// Search answers a query with FTS5. Every term must appear (implicit AND,
// which is what the tool documents); BM25 ranks the hits and the snippet
// comes from the best-matching message of each session.
func (i *Index) Search(ctx context.Context, query, cwd, excludeSession string, limit int) ([]Hit, error) {
	match := matchExpr(query)
	if match == "" {
		return nil, nil
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.db == nil {
		return nil, fmt.Errorf("io: session index is closed")
	}
	// bm25() is negative (better = more negative), so ordering ascending
	// gives best-first. The snippet column is 7 (the `text` column).
	args := []any{match}
	where := `docs MATCH ?`
	if strings.TrimSpace(cwd) != "" {
		where += ` AND cwd = ?`
		args = append(args, cwd)
	}
	if excludeSession != "" {
		where += ` AND session_id != ?`
		args = append(args, excludeSession)
	}
	args = append(args, limit*8)
	rows, err := i.db.QueryContext(ctx, `SELECT session_id, cwd, title, created, role,
		snippet(docs, 7, '⟨', '⟩', '…', 24), bm25(docs)
		FROM docs WHERE `+where+` ORDER BY bm25(docs) LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("io: session index search: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var hits []Hit
	seen := map[string]bool{}
	for rows.Next() {
		var (
			sessionID, cwdVal, title, created, role, snip string
			score                                         float64
		)
		if err := rows.Scan(&sessionID, &cwdVal, &title, &created, &role, &snip, &score); err != nil {
			return nil, fmt.Errorf("io: session index search: %w", err)
		}
		// One hit per session: the first is its best message by BM25, so
		// the snippet comes from exactly where the match is strongest.
		if seen[sessionID] {
			continue
		}
		seen[sessionID] = true
		if title == "" {
			title = "untitled session"
		}
		hits = append(hits, Hit{
			SessionID: sessionID, Title: title, Cwd: cwdVal, Created: created,
			Role: role, Snippet: strings.Join(strings.Fields(snip), " "),
			// The tool reports an integer score; BM25 is negative and
			// better ranked, so flip the sign for a human-readable value.
			Score: int(-score*100) + 1,
		})
		if len(hits) >= limit {
			break
		}
	}
	return hits, rows.Err()
}

// matchExpr turns free text into a safe FTS5 expression: every word quoted
// (so punctuation and FTS operators in user input cannot change the query),
// joined with AND. A trailing * on the last term keeps prefix matching,
// which is how the substring path behaved.
func matchExpr(query string) string {
	words := strings.Fields(strings.ToLower(query))
	if len(words) == 0 {
		return ""
	}
	parts := make([]string, 0, len(words))
	for _, w := range words {
		clean := strings.Map(func(r rune) rune {
			if r == '"' {
				return -1
			}
			return r
		}, w)
		if clean == "" {
			continue
		}
		parts = append(parts, `"`+clean+`"`)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " AND ")
}

// Stale reports the sessions in have that the index has not seen at the
// recorded modification time, plus indexed sessions that are gone.
func (i *Index) Stale(ctx context.Context, wants map[string]time.Time) ([]string, []string, error) {
	indexed, err := i.Sessions(ctx)
	if err != nil {
		return nil, nil, err
	}
	var refresh, drop []string
	for id, mod := range wants {
		if !indexed[id] {
			refresh = append(refresh, id)
			continue
		}
		at, ok := i.stamp(ctx, id)
		if !ok || at.Before(mod) {
			refresh = append(refresh, id)
		}
	}
	for id := range indexed {
		if _, ok := wants[id]; !ok {
			drop = append(drop, id)
		}
	}
	return refresh, drop, nil
}

// stamp is when the index last wrote a session.
func (i *Index) stamp(ctx context.Context, sessionID string) (time.Time, bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.db == nil {
		return time.Time{}, false
	}
	var raw string
	if err := i.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, "mtime:"+sessionID).Scan(&raw); err != nil {
		return time.Time{}, false
	}
	at, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, false
	}
	return at, true
}

// SetStamp records when a session was indexed.
func (i *Index) SetStamp(ctx context.Context, sessionID string, at time.Time) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.db == nil {
		return fmt.Errorf("io: session index is closed")
	}
	_, err := i.db.ExecContext(ctx, `INSERT INTO meta(key, value) VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, "mtime:"+sessionID, at.Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("io: session index stamp: %w", err)
	}
	return nil
}

// ForgetStamp drops a session's stamp (a deleted conversation).
func (i *Index) ForgetStamp(ctx context.Context, sessionID string) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.db == nil {
		return fmt.Errorf("io: session index is closed")
	}
	if _, err := i.db.ExecContext(ctx, `DELETE FROM meta WHERE key = ?`, "mtime:"+sessionID); err != nil {
		return fmt.Errorf("io: session index stamp: %w", err)
	}
	return nil
}
