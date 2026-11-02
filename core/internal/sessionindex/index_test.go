package sessionindex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Ady-JK/paladin/core/internal/transcript"
)

func openIndex(t *testing.T) *Index {
	t.Helper()
	idx, err := Open(filepath.Join(t.TempDir(), "sessions.fts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	return idx
}

func docs(sessionID, cwd string, texts ...string) []Doc {
	out := make([]Doc, 0, len(texts))
	for i, text := range texts {
		out = append(out, Doc{
			Key: sessionID + ":" + string(rune('a'+i)), SessionID: sessionID, Cwd: cwd,
			Title: "a session", Role: "user", Text: text, Seq: i, Created: "2026-01-01T00:00:00Z",
		})
	}
	return out
}

func TestSearchRanksAndSnippets(t *testing.T) {
	idx := openIndex(t)
	ctx := context.Background()
	if err := idx.ReplaceSession(ctx, "s1", docs("s1", "/work/a",
		"we discussed the litellm proxy port",
		"the litellm proxy port is 4000, and the proxy config lives in litellm.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := idx.ReplaceSession(ctx, "s2", docs("s2", "/work/a", "unrelated chatter about lunch")); err != nil {
		t.Fatal(err)
	}
	hits, err := idx.Search(ctx, "litellm proxy port", "/work/a", "", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].SessionID != "s1" {
		t.Fatalf("hits = %+v", hits)
	}
	if !strings.Contains(hits[0].Snippet, "litellm") {
		t.Fatalf("snippet misses the match: %q", hits[0].Snippet)
	}
}

func TestSearchRequiresEveryTerm(t *testing.T) {
	idx := openIndex(t)
	ctx := context.Background()
	if err := idx.ReplaceSession(ctx, "s1", docs("s1", "/w", "the proxy port is 4000")); err != nil {
		t.Fatal(err)
	}
	if hits, _ := idx.Search(ctx, "proxy 4000", "/w", "", 5); len(hits) != 1 {
		t.Fatalf("matching query returned %+v", hits)
	}
	if hits, _ := idx.Search(ctx, "proxy 9999", "/w", "", 5); len(hits) != 0 {
		t.Fatalf("query with an absent term returned %+v", hits)
	}
}

func TestSearchScopesByFolderAndExcludesSelf(t *testing.T) {
	idx := openIndex(t)
	ctx := context.Background()
	_ = idx.ReplaceSession(ctx, "a1", docs("a1", "/work/a", "shared token rotation note"))
	_ = idx.ReplaceSession(ctx, "b1", docs("b1", "/work/b", "shared token rotation note"))
	if hits, _ := idx.Search(ctx, "token rotation", "/work/a", "", 5); len(hits) != 1 || hits[0].SessionID != "a1" {
		t.Fatalf("folder scope leaked: %+v", hits)
	}
	if hits, _ := idx.Search(ctx, "token rotation", "", "", 5); len(hits) != 2 {
		t.Fatalf("global scope = %+v", hits)
	}
	if hits, _ := idx.Search(ctx, "token rotation", "", "a1", 5); len(hits) != 1 || hits[0].SessionID != "b1" {
		t.Fatalf("self exclusion failed: %+v", hits)
	}
}

func TestOneHitPerSession(t *testing.T) {
	idx := openIndex(t)
	ctx := context.Background()
	_ = idx.ReplaceSession(ctx, "s1", docs("s1", "/w",
		"alpha beta gamma", "alpha beta", "alpha", "alpha beta gamma delta"))
	hits, err := idx.Search(ctx, "alpha", "", "", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("per-session collapse failed: %+v", hits)
	}
}

func TestQueryPunctuationCannotBreakTheExpression(t *testing.T) {
	idx := openIndex(t)
	ctx := context.Background()
	_ = idx.ReplaceSession(ctx, "s1", docs("s1", "/w", "the proxy listens on port 4000"))
	for _, q := range []string{`" OR 1=1 --`, `proxy AND`, `*`, `(((`} {
		if _, err := idx.Search(ctx, q, "", "", 5); err != nil {
			// A query that yields no terms returns no hits; it must never
			// return a SQL/FTS error.
			if !strings.Contains(err.Error(), "syntax") {
				t.Fatalf("query %q errored: %v", q, err)
			}
		}
	}
}

func TestReplaceSessionDropsStaleRows(t *testing.T) {
	idx := openIndex(t)
	ctx := context.Background()
	_ = idx.ReplaceSession(ctx, "s1", docs("s1", "/w", "old wording about vectors"))
	_ = idx.ReplaceSession(ctx, "s1", docs("s1", "/w", "new wording about embeddings"))
	if hits, _ := idx.Search(ctx, "vectors", "", "", 5); len(hits) != 0 {
		t.Fatalf("stale row survived reindex: %+v", hits)
	}
	if hits, _ := idx.Search(ctx, "embeddings", "", "", 5); len(hits) != 1 {
		t.Fatalf("new row missing: %+v", hits)
	}
}

func TestIndexReopensWithItsContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.fts.db")
	idx, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := idx.ReplaceSession(ctx, "s1", docs("s1", "/w", "durable across restarts")); err != nil {
		t.Fatal(err)
	}
	if err := idx.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = again.Close() }()
	hits, err := again.Search(ctx, "durable restarts", "", "", 5)
	if err != nil || len(hits) != 1 {
		t.Fatalf("reopen lost the index: %+v %v", hits, err)
	}
}

func TestCorruptIndexRebuildsInsteadOfFailing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.fts.db")
	if err := os.WriteFile(path, []byte("this is not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	idx, err := Open(path)
	if err != nil {
		t.Fatalf("corrupt cache must be rebuilt, got %v", err)
	}
	defer func() { _ = idx.Close() }()
	if _, err := idx.Search(context.Background(), "anything", "", "", 5); err != nil {
		t.Fatalf("rebuilt index unusable: %v", err)
	}
}

// --- the indexer: transcripts are the source of truth --------------------

func TestRefreshIndexesChangedSessionsAndDropsDeletedOnes(t *testing.T) {
	dataDir := t.TempDir()
	store := transcript.NewStore(filepath.Join(dataDir, "sessions"))
	ctx := context.Background()

	keep, err := store.Create("/work/a", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := keep.Append(transcript.RoleUser, "how do we run the migrations"); err != nil {
		t.Fatal(err)
	}
	gone, err := store.Create("/work/a", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := gone.Append(transcript.RoleUser, "delete me later"); err != nil {
		t.Fatal(err)
	}

	idx := openIndex(t)
	n, err := idx.Refresh(ctx, Source{Store: store, DataDir: dataDir})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("indexed %d sessions, want 2", n)
	}
	if hits, _ := idx.Search(ctx, "migrations", "", "", 5); len(hits) != 1 || hits[0].SessionID != keep.Meta.SessionID {
		t.Fatalf("hits = %+v", hits)
	}

	// A second refresh with nothing changed writes nothing.
	if n, err := idx.Refresh(ctx, Source{Store: store, DataDir: dataDir}); err != nil || n != 0 {
		t.Fatalf("idle refresh wrote %d (%v)", n, err)
	}

	if err := store.Delete("/work/a", gone.Meta.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := idx.Refresh(ctx, Source{Store: store, DataDir: dataDir}); err != nil {
		t.Fatal(err)
	}
	if hits, _ := idx.Search(ctx, "delete", "", "", 5); len(hits) != 0 {
		t.Fatalf("deleted session still indexed: %+v", hits)
	}
}

func TestRefreshPicksUpAnEditedSession(t *testing.T) {
	dataDir := t.TempDir()
	store := transcript.NewStore(filepath.Join(dataDir, "sessions"))
	ctx := context.Background()
	sess, err := store.Create("/work/a", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.Append(transcript.RoleUser, "first message"); err != nil {
		t.Fatal(err)
	}
	idx := openIndex(t)
	if _, err := idx.Refresh(ctx, Source{Store: store, DataDir: dataDir}); err != nil {
		t.Fatal(err)
	}
	if hits, _ := idx.Search(ctx, "second", "", "", 5); len(hits) != 0 {
		t.Fatal("found text that does not exist yet")
	}

	if err := sess.Append(transcript.RoleAssistant, "the second message"); err != nil {
		t.Fatal(err)
	}
	// The transcript's mtime drives the check; make sure it moved even on
	// a coarse-grained filesystem.
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(filepath.Join(sess.Dir, "messages.jsonl"), future, future); err != nil {
		t.Fatal(err)
	}
	if _, err := idx.Refresh(ctx, Source{Store: store, DataDir: dataDir}); err != nil {
		t.Fatal(err)
	}
	if hits, _ := idx.Search(ctx, "second", "", "", 5); len(hits) != 1 {
		t.Fatalf("edited session not reindexed: %+v", hits)
	}
}

func TestRefreshSkipsReplayedMessages(t *testing.T) {
	dataDir := t.TempDir()
	store := transcript.NewStore(filepath.Join(dataDir, "sessions"))
	ctx := context.Background()
	sess, err := store.Create("/work/a", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendMessage(transcript.Message{Role: transcript.RoleUser, Content: "live question"}); err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendMessage(transcript.Message{Role: transcript.RoleUser, Content: "replayed parent text", Replay: true}); err != nil {
		t.Fatal(err)
	}
	idx := openIndex(t)
	if _, err := idx.Refresh(ctx, Source{Store: store, DataDir: dataDir}); err != nil {
		t.Fatal(err)
	}
	if hits, _ := idx.Search(ctx, "replayed", "", "", 5); len(hits) != 0 {
		t.Fatalf("replayed text indexed (doubles every fork): %+v", hits)
	}
	if hits, _ := idx.Search(ctx, "live", "", "", 5); len(hits) != 1 {
		t.Fatalf("live message missing: %+v", hits)
	}
}
