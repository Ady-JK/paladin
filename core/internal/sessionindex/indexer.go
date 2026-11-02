// indexer.go keeps the session FTS index in step with the transcripts.
//
// Transcripts on disk stay authoritative; the index is a derived cache, so
// this pass is deliberately forgiving: it reindexes sessions whose
// transcript changed, drops rows for sessions that are gone, and gives up
// quietly on any single damaged session rather than failing the whole run.
//
// It runs on demand (the first session_search of a turn calls Refresh) and
// never in the request path of a turn, so a slow filesystem costs latency
// on recall only.

package sessionindex

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Ady-JK/paladin/core/internal/transcript"
)

// Source is the transcript side this package needs.
type Source struct {
	Store *transcript.Store
	// DataDir is where sessions live; it is walked to find every
	// transcript, not just the current folder's.
	DataDir string
}

// Refresh reindexes every session under source.DataDir whose transcript
// changed since it was last indexed, and drops rows for deleted sessions.
// It returns how many sessions it wrote.
func (i *Index) Refresh(ctx context.Context, source Source) (int, error) {
	if source.Store == nil || source.DataDir == "" {
		return 0, nil
	}
	sessions, err := findSessions(source.DataDir)
	if err != nil {
		return 0, err
	}
	wants := make(map[string]time.Time, len(sessions))
	for _, s := range sessions {
		wants[s.id] = s.mtime
	}
	refresh, drop, err := i.Stale(ctx, wants)
	if err != nil {
		return 0, err
	}
	for _, id := range drop {
		_ = i.DeleteSession(ctx, id)
		_ = i.ForgetStamp(ctx, id)
	}
	wrote := 0
	for _, id := range refresh {
		if ctx.Err() != nil {
			return wrote, ctx.Err()
		}
		s := sessions[indexOf(sessions, id)]
		docs, err := i.docsFor(source.Store, s)
		if err != nil {
			// One unreadable transcript must not stop the pass.
			continue
		}
		if err := i.ReplaceSession(ctx, id, docs); err != nil {
			return wrote, err
		}
		if err := i.SetStamp(ctx, id, s.mtime); err != nil {
			return wrote, err
		}
		wrote++
	}
	return wrote, nil
}

func indexOf(all []sessionFile, id string) int {
	for i, s := range all {
		if s.id == id {
			return i
		}
	}
	return 0
}

// sessionFile is one transcript file found on disk.
type sessionFile struct {
	id    string
	cwd   string
	dir   string
	mtime time.Time
}

// findSessions walks <data>/sessions for transcripts. Layout:
// sessions/<key>/<sessionId>/transcript.jsonl, with the folder path beside
// it — the same shape the transcript store writes.
func findSessions(dataDir string) ([]sessionFile, error) {
	root := filepath.Join(dataDir, "sessions")
	var out []sessionFile
	projects, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("io: read sessions: %w", err)
	}
	for _, p := range projects {
		if !p.IsDir() {
			continue
		}
		sessions, err := os.ReadDir(filepath.Join(root, p.Name()))
		if err != nil {
			continue
		}
		for _, s := range sessions {
			if !s.IsDir() {
				continue
			}
			dir := filepath.Join(root, p.Name(), s.Name())
			info, err := os.Stat(filepath.Join(dir, "messages.jsonl"))
			if err != nil {
				continue
			}
			out = append(out, sessionFile{id: s.Name(), dir: dir, cwd: readCwd(dir), mtime: info.ModTime()})
		}
	}
	return out, nil
}

// readCwd reads a session's folder from its meta.json. It is only used
// when falling back to a folder-scoped load, so a failure is harmless.
func readCwd(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return ""
	}
	var meta struct {
		Cwd string `json:"cwd"`
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return ""
	}
	return meta.Cwd
}

// docsFor reads one session and produces its indexable messages: user and
// assistant text only, with the session's title and folder repeated on
// each row so a hit can be reported without a second lookup.
func (i *Index) docsFor(store *transcript.Store, s sessionFile) ([]Doc, error) {
	sess, err := store.Load("", s.id)
	if err != nil {
		// A folder-scoped store may need the cwd; try the folder recorded
		// beside the session.
		if sess, err = store.Load(s.cwd, s.id); err != nil {
			return nil, err
		}
	}
	msgs, err := sess.History()
	if err != nil {
		return nil, err
	}
	title := sess.Meta.Title
	created := sess.Meta.CreatedAt.Format(time.RFC3339)
	cwd := sess.Meta.Cwd
	docs := make([]Doc, 0, len(msgs))
	for n, m := range msgs {
		if m.Replay || (m.Role != transcript.RoleUser && m.Role != transcript.RoleAssistant) {
			continue
		}
		docs = append(docs, Doc{
			Key: fmt.Sprintf("%s:%d", sess.Meta.SessionID, n), SessionID: sess.Meta.SessionID,
			Cwd: cwd, Title: title, Role: string(m.Role), Text: m.Content, Seq: n, Created: created,
		})
	}
	return docs, nil
}
