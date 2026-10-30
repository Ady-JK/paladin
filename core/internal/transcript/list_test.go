package transcript

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// scans records which transcripts List read, and from what offset.
type scans struct {
	mu  sync.Mutex
	got map[string][]int64
}

func watchScans(t *testing.T) *scans {
	t.Helper()
	s := &scans{got: map[string][]int64{}}
	onScan = func(dir string, from int64) {
		s.mu.Lock()
		s.got[filepath.Base(dir)] = append(s.got[filepath.Base(dir)], from)
		s.mu.Unlock()
	}
	t.Cleanup(func() { onScan = nil })
	return s
}

func (s *scans) of(id string) []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.got[id]
}

func (s *scans) total() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, v := range s.got {
		n += len(v)
	}
	return n
}

// newSessions creates n sessions in /p, oldest first, each with a turn.
func newSessions(t *testing.T, store *Store, n int) []Session {
	t.Helper()
	out := make([]Session, n)
	base := time.Now().Add(-time.Hour)
	for i := range n {
		sess, err := store.Create("/p", "prov", "m")
		if err != nil {
			t.Fatal(err)
		}
		sess.Meta.CreatedAt = base.Add(time.Duration(i) * time.Minute)
		if err := sess.SaveMeta(); err != nil {
			t.Fatal(err)
		}
		if err := sess.AppendTurn([]Message{UserMessage(fmt.Sprintf("q%d", i)), AssistantMessage("a")}); err != nil {
			t.Fatal(err)
		}
		out[i] = sess
	}
	return out
}

// Only the sessions List returns have their transcripts read.
func TestListReadsTranscriptsOnlyAfterTheLimit(t *testing.T) {
	store := NewStore(t.TempDir())
	sessions := newSessions(t, store, 10)
	seen := watchScans(t)
	infos, err := store.List("/p", 3)
	if err != nil || len(infos) != 3 {
		t.Fatalf("list = %d, %v", len(infos), err)
	}
	if infos[0].Meta.SessionID != sessions[9].Meta.SessionID {
		t.Fatalf("newest first: got %s", infos[0].Meta.SessionID)
	}
	if n := seen.total(); n != 3 {
		t.Fatalf("scanned %d transcripts for a limit of 3", n)
	}
	if infos[0].MessageCount != 2 || infos[0].Title != "q9" {
		t.Fatalf("details = %+v", infos[0])
	}
}

// A cached summary answers until the transcript grows; then only the
// new tail is read.
func TestListCachesSummariesAndReadsOnlyNewTurns(t *testing.T) {
	store := NewStore(t.TempDir())
	sess := newSessions(t, store, 1)[0]
	if _, err := store.List("/p", 0); err != nil {
		t.Fatal(err)
	}
	seen := watchScans(t)
	if _, err := store.List("/p", 0); err != nil {
		t.Fatal(err)
	}
	if n := seen.total(); n != 0 {
		t.Fatalf("an unchanged session was rescanned %d times", n)
	}
	st, _ := os.Stat(filepath.Join(sess.Dir, "messages.jsonl"))
	if err := sess.AppendTurn([]Message{UserMessage("more"), AssistantMessage("b")}); err != nil {
		t.Fatal(err)
	}
	infos, err := store.List("/p", 0)
	if err != nil || infos[0].MessageCount != 4 || infos[0].Title != "q0" {
		t.Fatalf("after a turn: %+v, %v", infos, err)
	}
	if got := seen.of(sess.Meta.SessionID); len(got) != 1 || got[0] != st.Size() {
		t.Fatalf("scans after a turn = %v, want one from %d", got, st.Size())
	}
}

// Undo rewrites the transcript, so the summary is rebuilt in full.
func TestTruncateRebuildsTheSummary(t *testing.T) {
	store := NewStore(t.TempDir())
	sess := newSessions(t, store, 1)[0]
	_ = sess.AppendTurn([]Message{UserMessage("more"), AssistantMessage("b")})
	if _, err := store.List("/p", 0); err != nil {
		t.Fatal(err)
	}
	if err := sess.Truncate(2); err != nil {
		t.Fatal(err)
	}
	seen := watchScans(t)
	infos, err := store.List("/p", 0)
	if err != nil || infos[0].MessageCount != 2 {
		t.Fatalf("after undo: %+v, %v", infos, err)
	}
	if got := seen.of(sess.Meta.SessionID); len(got) != 1 || got[0] != 0 {
		t.Fatalf("scans after undo = %v, want one full scan", got)
	}
}

// A corrupt summary is ignored, not trusted.
func TestCorruptSummaryIsRebuilt(t *testing.T) {
	store := NewStore(t.TempDir())
	sess := newSessions(t, store, 1)[0]
	_, _ = store.List("/p", 0)
	if err := os.WriteFile(filepath.Join(sess.Dir, "summary.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	infos, err := store.List("/p", 0)
	if err != nil || infos[0].MessageCount != 2 || infos[0].Damaged != "" {
		t.Fatalf("with a corrupt summary: %+v, %v", infos, err)
	}
}

// Sessions that cannot be read are listed as damaged instead of
// vanishing.
func TestDamagedSessionsAreListedWithTheReason(t *testing.T) {
	store := NewStore(t.TempDir())
	sessions := newSessions(t, store, 3)
	if err := os.WriteFile(filepath.Join(sessions[0].Dir, "meta.json"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessions[1].Dir, "messages.jsonl"), []byte("{bad}\n{\"role\":\"user\",\"content\":\"x\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Stray folders are not sessions.
	if err := os.MkdirAll(filepath.Join(filepath.Dir(sessions[0].Dir), "not-a-session"), 0o700); err != nil {
		t.Fatal(err)
	}
	infos, err := store.List("/p", 0)
	if err != nil || len(infos) != 3 {
		t.Fatalf("list = %d, %v", len(infos), err)
	}
	damaged := map[string]string{}
	for _, info := range infos {
		damaged[info.Meta.SessionID] = info.Damaged
	}
	if damaged[sessions[0].Meta.SessionID] == "" || damaged[sessions[1].Meta.SessionID] == "" {
		t.Fatalf("damaged = %v", damaged)
	}
	if damaged[sessions[2].Meta.SessionID] != "" {
		t.Fatalf("healthy session marked damaged: %v", damaged)
	}
}

// ListMeta never reads transcripts and skips what it cannot read.
func TestListMetaReadsNoTranscripts(t *testing.T) {
	store := NewStore(t.TempDir())
	sessions := newSessions(t, store, 4)
	_ = os.WriteFile(filepath.Join(sessions[0].Dir, "meta.json"), []byte("{broken"), 0o600)
	seen := watchScans(t)
	metas, err := store.ListMeta("")
	if err != nil || len(metas) != 3 {
		t.Fatalf("metas = %d, %v", len(metas), err)
	}
	if seen.total() != 0 {
		t.Fatal("ListMeta read transcripts")
	}
}

// BenchmarkListLimited lists the newest 50 of 500 sessions with warm
// summaries (the sidebar's refresh after a turn).
func BenchmarkListLimited(b *testing.B) {
	store := NewStore(b.TempDir())
	for i := range 500 {
		sess, err := store.Create("/p", "prov", "m")
		if err != nil {
			b.Fatal(err)
		}
		for j := range 20 {
			_ = sess.AppendTurn([]Message{UserMessage(fmt.Sprintf("q%d-%d", i, j)), AssistantMessage("some answer text that is a bit longer")})
		}
	}
	if _, err := store.List("/p", 50); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for range b.N {
		if _, err := store.List("/p", 50); err != nil {
			b.Fatal(err)
		}
	}
}
