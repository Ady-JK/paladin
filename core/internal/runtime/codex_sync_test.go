package runtime

import (
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Ady-JK/paladin/core/internal/codex"
	"github.com/Ady-JK/paladin/core/internal/media"
	"github.com/Ady-JK/paladin/core/internal/session"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

func TestCodexCompactionProjectionKeepsTheCommandAcrossSync(t *testing.T) {
	raw := codex.Decode([]byte(`{"id":"tu-c","status":"completed","items":[{"type":"contextCompaction","id":"cc-1"}]}`))
	user := transcript.UserMessage("/compact")
	user.HarnessTurn = "tu-c"
	stats := &transcript.TurnStats{DurationMs: 1234}
	// Reproject old sessions too: the old projection saved ordinary reply
	// text and hashed only Codex's source, without a projection version.
	encoded, _ := json.Marshal(raw)
	seen := map[string][32]byte{"tu-c": sha256.Sum256(encoded)}
	turns := []transcript.Turn{{Messages: []transcript.Message{user,
		{Role: transcript.RoleAssistant, Content: "Context compacted."}}, Stats: stats}}
	for range 2 {
		turns, seen = reconcileCodexTurns(turns, []codex.Raw{raw}, seen)
		if len(turns) != 1 || len(turns[0].Messages) != 2 || turns[0].Stats != stats {
			t.Fatalf("compaction turn lost or duplicated: %+v", turns)
		}
		msgs := turns[0].Messages
		if msgs[0].Content != "/compact" || msgs[0].HarnessTurn != "tu-c" || !msgs[1].IsCompaction() || msgs[1].Content != "" {
			t.Fatalf("expected command and durable divider: %+v", msgs)
		}
	}
}

func TestImportedCodexCompactionSplitsSurroundingText(t *testing.T) {
	raw := codex.Decode([]byte(`{"items":[{"type":"agentMessage","id":"before","text":"Before."},{"type":"contextCompaction","id":"cc-1"},{"type":"agentMessage","id":"after","text":"After."}]}`))
	msgs := codexTurnMessages(raw)
	if len(msgs) != 3 || msgs[0].Content != "Before." || !msgs[1].IsCompaction() || msgs[1].Content != "" || msgs[2].Content != "After." {
		t.Fatalf("compaction must separate the surrounding text: %+v", msgs)
	}
}

func TestCodexCompactionSurvivesHistorySyncAndReopen(t *testing.T) {
	rt, _, _ := codexRuntime(t, "chat")
	path := filepath.Join(t.TempDir(), "codex-history.json")
	t.Setenv("PALADIN_FAKE_CODEX_HISTORY", path)
	id, cwd := openCodex(t, rt)
	codexReply(t, rt, id, cwd, "/compact")
	sess, err := rt.transcripts.Load(cwd, id)
	if err != nil {
		t.Fatal(err)
	}
	raw := codex.Decode([]byte(`{"id":"tu-c","status":"completed","items":[{"type":"contextCompaction","id":"cc-1"}]}`))
	data, _ := json.Marshal(map[string][]codex.Raw{sess.Meta.Harness.ThreadID: {raw}})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		rt.codexSync.Delete(id) // reload the saved checkpoint as on app restart
		if _, err := rt.OpenSession(id, cwd); err != nil {
			t.Fatal(err)
		}
		rows, err := rt.SessionHistory(id, cwd)
		if err != nil || len(rows) != 2 || rows[0].Content != "/compact" || derefString(rows[1].Kind) != "compaction" || rows[1].Content != "" {
			t.Fatalf("command and divider did not survive sync/reopen: %+v, %v", rows, err)
		}
	}
}

func writeCodexHistory(t *testing.T, path string, turns []any) {
	t.Helper()
	data, err := json.Marshal(map[string]any{"th-old": turns})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCodexSyncUpdatesTurnsOnceAndFollowsExternalRewind(t *testing.T) {
	rt, _, _ := codexRuntime(t, "chat")
	cwd := t.TempDir()
	t.Setenv("PALADIN_FAKE_CODEX_CWD", cwd)
	path := filepath.Join(t.TempDir(), "codex-history.json")
	t.Setenv("PALADIN_FAKE_CODEX_HISTORY", path)
	turns := []any{externalCodexTurn("tu-ext", "Partial answer")}
	writeCodexHistory(t, path, turns)
	opened, err := rt.ImportCodexThread("th-old", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.SyncSession(opened.SessionID, cwd); err != nil {
		t.Fatal(err)
	}
	turns[0] = externalCodexTurn("tu-ext", "Completed answer")
	turns = append(turns, externalCodexTurn("tu-later", "Later turn"))
	writeCodexHistory(t, path, turns)
	if changed, err := rt.SyncSession(opened.SessionID, cwd); err != nil || !changed {
		t.Fatalf("sync: changed=%v err=%v", changed, err)
	}
	if changed, err := rt.SyncSession(opened.SessionID, cwd); err != nil || changed {
		t.Fatalf("unchanged history was rewritten: changed=%v err=%v", changed, err)
	}
	writeCodexHistory(t, path, turns[:1])
	rt.codexSync.Delete(opened.SessionID) // simulate a restart before the external rewind is read
	if changed, err := rt.SyncSession(opened.SessionID, cwd); err != nil || !changed {
		t.Fatalf("rewind: %v %v", changed, err)
	}
	sess, _ := rt.transcripts.Load(cwd, opened.SessionID)
	saved, _ := sess.HistoryTurns()
	if len(saved) != 1 || saved[0].Messages[len(saved[0].Messages)-1].Content != "Completed answer" {
		t.Fatalf("wrong synced history: %+v", saved)
	}
}

func TestSendingInPaladinRefreshesExternalHistoryAndKeepsTheCodexThread(t *testing.T) {
	rt, _, fake := codexRuntime(t, "chat")
	cwd := t.TempDir()
	t.Setenv("PALADIN_FAKE_CODEX_CWD", cwd)
	path := filepath.Join(t.TempDir(), "codex-history.json")
	t.Setenv("PALADIN_FAKE_CODEX_HISTORY", path)
	turns := fakeThreadTurns("th-old")
	writeCodexHistory(t, path, turns)
	opened, err := rt.ImportCodexThread("th-old", "")
	if err != nil {
		t.Fatal(err)
	}
	turns = append(turns, externalCodexTurn("tu-outside", "Outside continuation"))
	writeCodexHistory(t, path, turns)
	info := <-submitCodex(t, rt, opened.SessionID, cwd, "continue here")
	if info.Error != "" {
		t.Fatal(info.Error)
	}
	sess, _ := rt.transcripts.Load(cwd, opened.SessionID)
	saved, _ := sess.History()
	var texts []string
	for _, m := range saved {
		texts = append(texts, m.Content)
	}
	joined := strings.Join(texts, "\n")
	if !strings.Contains(joined, "Outside continuation") || strings.Index(joined, "Outside continuation") > strings.Index(joined, "continue here") {
		t.Fatal("the external continuation must precede Paladin's next turn")
	}
	log := fakeLog(t, fake)
	if strings.Contains(log, "thread/start") || !strings.Contains(log, "turn/start thread=th-old") {
		t.Fatalf("started a different Codex thread: %s", log)
	}
	// A saved local turn not flushed by Codex yet is retained, with attachments.
	if _, err := rt.SyncSession(opened.SessionID, cwd); err != nil {
		t.Fatal(err)
	}
	saved, _ = sess.History()
	count := 0
	for _, m := range saved {
		if m.Content == "continue here" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("local turn lost or duplicated: %d", count)
	}
}

func TestCodexMirrorPreservesLocalAttachmentsAndStats(t *testing.T) {
	user := transcript.UserMessage("continued in Codex")
	user.HarnessTurn = "tu-1"
	user.Attachments = []media.Attachment{{Name: "result.txt", Path: "/w/result.txt"}}
	stats := &transcript.TurnStats{DurationMs: 1234, Tokens: 50}
	local := []transcript.Turn{{Messages: []transcript.Message{user}, Stats: stats}}
	raw, _ := json.Marshal(externalCodexTurn("tu-1", "done"))
	next, _ := reconcileCodexTurns(local, []codex.Raw{codex.Decode(raw)}, nil)
	if len(next) != 1 || next[0].Stats != stats || len(next[0].Messages[0].Attachments) != 1 {
		t.Fatalf("local metadata lost: %+v", next)
	}
}

func TestCodexSyncDoesNotTouchAPaladinTurnInProgress(t *testing.T) {
	rt, log, _ := codexRuntime(t, "hang")
	cwd := t.TempDir()
	t.Setenv("PALADIN_FAKE_CODEX_CWD", cwd)
	opened, err := rt.ImportCodexThread("th-old", "")
	if err != nil {
		t.Fatal(err)
	}
	done := submitCodex(t, rt, opened.SessionID, cwd, "keep working")
	log.waitFor(t, "running command", func(evs []TurnEvent) bool {
		for _, e := range evs {
			if e.Tool != nil && e.Tool.Status == "running" {
				return true
			}
		}
		return false
	})
	if changed, err := rt.SyncSession(opened.SessionID, cwd); err != nil || changed {
		t.Fatalf("synced a running turn: %v %v", changed, err)
	}
	rt.hub.StopSession(opened.SessionID, cwd)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not stop")
	}
}

func externalCodexTurn(id, answer string) map[string]any {
	return map[string]any{"id": id, "status": "completed", "startedAt": 1794900000, "items": []any{
		map[string]any{"type": "userMessage", "id": id + "-u", "content": []any{map[string]any{"type": "text", "text": "continued in Codex"}}},
		map[string]any{"type": "agentMessage", "id": id + "-a", "text": answer, "phase": "final_answer"},
	}}
}

func TestOpeningLinkedCodexSessionReadsExternalTurns(t *testing.T) {
	rt, log, _ := codexRuntime(t, "chat")
	cwd := t.TempDir()
	t.Setenv("PALADIN_FAKE_CODEX_CWD", cwd)
	path := filepath.Join(t.TempDir(), "codex-history.json")
	t.Setenv("PALADIN_FAKE_CODEX_HISTORY", path)
	turns := fakeThreadTurns("th-old")
	writeCodexHistory(t, path, turns)
	opened, err := rt.ImportCodexThread("th-old", "")
	if err != nil {
		t.Fatal(err)
	}
	turns = append(turns, externalCodexTurn("tu-external", "The new answer from Codex."))
	writeCodexHistory(t, path, turns)
	if _, err := rt.OpenSession(opened.SessionID, cwd); err != nil {
		t.Fatal(err)
	}
	// The continuation is read after the open returns and announced as a refresh.
	log.waitFor(t, "the reopened session to refresh", func(events []TurnEvent) bool {
		for _, e := range events {
			if e.Event == "session/refresh" && e.SessionID == opened.SessionID {
				return true
			}
		}
		return false
	})
	att, err := rt.Attach(opened.SessionID, cwd, 0)
	if err != nil {
		t.Fatal(err)
	}
	page := att.History.(session.HistoryPage)
	if len(page.Messages) == 0 || page.Messages[len(page.Messages)-1].Content != "The new answer from Codex." {
		t.Fatal("reopening the linked session did not fetch the continuation from Codex")
	}
	// Re-importing uses the same session and refreshes it too.
	turns = append(turns, externalCodexTurn("tu-external-2", "And another continuation."))
	writeCodexHistory(t, path, turns)
	again, err := rt.ImportCodexThread("th-old", "")
	if err != nil || again.SessionID != opened.SessionID {
		t.Fatalf("reimport: %+v %v", again, err)
	}
	rows, err := rt.SessionHistory(opened.SessionID, cwd)
	if err != nil || !strings.Contains(rows[len(rows)-1].Content, "another continuation") {
		t.Fatalf("reimport did not refresh: %v", err)
	}
}

// Opening a linked chat must not wait for the external client's history: the
// transcript on disk opens at once, and a change found afterwards reaches the
// UI as a session/refresh event.
func TestOpeningALinkedSessionSyncsInTheBackground(t *testing.T) {
	rt, _, _ := codexRuntime(t, "chat")
	log := newEventLog()
	rt.SetTurnSink(log.sink)
	cwd := t.TempDir()
	t.Setenv("PALADIN_FAKE_CODEX_CWD", cwd)
	path := filepath.Join(t.TempDir(), "codex-history.json")
	t.Setenv("PALADIN_FAKE_CODEX_HISTORY", path)
	turns := []any{externalCodexTurn("tu-ext", "First answer")}
	writeCodexHistory(t, path, turns)
	imported, err := rt.ImportCodexThread("th-old", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.SyncSession(imported.SessionID, cwd); err != nil {
		t.Fatal(err)
	}
	// The external client moves on while the chat is closed.
	turns = append(turns, externalCodexTurn("tu-later", "Written while closed"))
	writeCodexHistory(t, path, turns)

	if _, err := rt.OpenSession(imported.SessionID, cwd); err != nil {
		t.Fatal(err)
	}
	log.waitFor(t, "a session/refresh once the background sync lands", func(events []TurnEvent) bool {
		for _, e := range events {
			if e.Event == "session/refresh" && e.SessionID == imported.SessionID {
				return true
			}
		}
		return false
	})
	sess, _ := rt.transcripts.Load(cwd, imported.SessionID)
	saved, _ := sess.HistoryTurns()
	if len(saved) != 2 {
		t.Fatalf("the background sync did not store the later turn: %d turns", len(saved))
	}
}

// A chat opened and deleted straight away must not have its directory
// recreated or blocked by the sync that open started in the background.
func TestDeletingAJustOpenedSessionWaitsForItsBackgroundSync(t *testing.T) {
	for i := 0; i < 5; i++ {
		rt, _, _ := codexRuntime(t, "chat")
		cwd := t.TempDir()
		t.Setenv("PALADIN_FAKE_CODEX_CWD", cwd)
		path := filepath.Join(t.TempDir(), "codex-history.json")
		t.Setenv("PALADIN_FAKE_CODEX_HISTORY", path)
		turns := []any{externalCodexTurn("tu-ext", "First answer")}
		writeCodexHistory(t, path, turns)
		imported, err := rt.ImportCodexThread("th-old", "")
		if err != nil {
			t.Fatal(err)
		}
		turns = append(turns, externalCodexTurn("tu-later", "Written while closed"))
		writeCodexHistory(t, path, turns)
		if _, err := rt.OpenSession(imported.SessionID, cwd); err != nil {
			t.Fatal(err)
		}
		if _, err := rt.DeleteSession(imported.SessionID, cwd); err != nil {
			t.Fatalf("round %d: delete right after open: %v", i, err)
		}
		time.Sleep(150 * time.Millisecond)
		if _, err := rt.transcripts.Load(cwd, imported.SessionID); err == nil {
			t.Fatalf("round %d: the session came back after it was deleted", i)
		}
	}
}
