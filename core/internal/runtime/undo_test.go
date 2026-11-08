package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Ady-JK/paladin/core/internal/agent"
	"github.com/Ady-JK/paladin/core/internal/config"
	"github.com/Ady-JK/paladin/core/internal/session"
	"github.com/Ady-JK/paladin/core/internal/snapshot"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

func BenchmarkUndoPreviewLongTranscript(b *testing.B) {
	dataDir := b.TempDir()
	rt := WithKeyStore(dataDir, config.NewConfig(dataDir), config.NewMemoryKeyStore())
	b.Cleanup(func() { rt.ShutdownTurns(time.Second) })
	rt.snaps = nil
	sess, err := rt.transcripts.Create(b.TempDir(), "", "")
	if err != nil {
		b.Fatal(err)
	}
	const turns = 5000
	var content strings.Builder
	for i := 0; i < turns; i++ {
		content.WriteString("{\"turn\":{\"messages\":[{\"role\":\"user\",\"content\":\"question\"},{\"role\":\"assistant\",\"content\":\"answer\"}]}}\n")
	}
	if err := os.WriteFile(filepath.Join(sess.Dir, "messages.jsonl"), []byte(content.String()), 0o600); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := rt.RevertPreview(sess.Meta.SessionID, sess.Meta.Cwd, (turns-1)*2); err != nil {
			b.Fatal(err)
		}
	}
}

func TestUndoCountsTurnsRatherThanSteeringMessages(t *testing.T) {
	rt, ws, sid := undoRuntime(t)
	rt.snaps = nil
	sess, err := rt.transcripts.Load(ws, sid)
	if err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 3; n++ {
		if err := sess.AppendTurn([]transcript.Message{transcript.UserMessage(fmt.Sprintf("turn-%d", n)), transcript.UserMessage("steer"), {Role: transcript.RoleAssistant, Content: "done"}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := rt.RevertPreview(sid, ws, 0); err != nil {
		t.Fatalf("three turns with steering should be undoable: %v", err)
	}
	fork, err := rt.ForkSession(sid, ws)
	if err != nil {
		t.Fatal(err)
	}
	forkSession, err := rt.transcripts.Load(ws, fork.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	history, err := readUndoHistory(forkSession)
	if err != nil || len(history.starts) != 3 {
		t.Fatalf("fork turn boundaries = %v, %v", history.starts, err)
	}
	if preview, err := rt.RevertPreview(sid, ws, 4); err != nil || preview.Index != 3 {
		t.Fatalf("steer preview = %+v, %v", preview, err)
	}
	if _, err := rt.RevertSession(sid, ws, 4); err != nil {
		t.Fatalf("revert second turn: %v", err)
	}
	turns, err := sess.HistoryTurns()
	if err != nil || len(turns) != 1 || len(turns[0].Messages) != 3 {
		t.Fatalf("turn grouping after revert = %+v, %v", turns, err)
	}
}

func TestUndoRetainsOnlyThreeTurnsPerSession(t *testing.T) {
	rt, ws, sid := undoRuntime(t,
		"echo one > a.txt", "", "echo two > a.txt", "", "echo three > a.txt", "", "echo four > a.txt", "")
	if err := os.WriteFile(filepath.Join(ws, "a.txt"), []byte("original\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	turn(t, rt, sid, ws, "first")
	firstSession, err := rt.transcripts.Load(ws, sid)
	if err != nil {
		t.Fatal(err)
	}
	firstSnapshots, err := readTurnSnapshots(firstSession.Dir)
	if err != nil || len(firstSnapshots) != 1 {
		t.Fatalf("first snapshots = %v, %v", firstSnapshots, err)
	}
	oldTree := firstSnapshots[0].Tree
	objects, err := filepath.Glob(filepath.Join(rt.dataDir, "snapshots", "*", "objects", oldTree[:2], oldTree[2:]))
	if err != nil || len(objects) != 1 {
		t.Fatalf("old tree objects = %v, %v", objects, err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(objects[0], old, old); err != nil {
		t.Fatal(err)
	}
	other, err := rt.OpenSession("", ws)
	if err != nil {
		t.Fatal(err)
	}
	turn(t, rt, other.SessionID, ws, "other chat")
	otherSession, err := rt.transcripts.Load(ws, other.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	otherSnapshots, err := readTurnSnapshots(otherSession.Dir)
	if err != nil || len(otherSnapshots) != 1 {
		t.Fatalf("other snapshots = %v, %v", otherSnapshots, err)
	}
	otherTree := otherSnapshots[0].Tree
	otherObjects, err := filepath.Glob(filepath.Join(rt.dataDir, "snapshots", "*", "objects", otherTree[:2], otherTree[2:]))
	if err != nil || len(otherObjects) != 1 {
		t.Fatalf("other tree objects = %v, %v", otherObjects, err)
	}
	if err := os.Chtimes(otherObjects[0], old, old); err != nil {
		t.Fatal(err)
	}
	for _, msg := range []string{"second", "third", "fourth"} {
		turn(t, rt, sid, ws, msg)
	}
	rows := userRows(t, rt, sid)
	list, err := readTurnSnapshots(firstSession.Dir)
	if err != nil || len(list) != 3 {
		t.Fatalf("retained snapshots = %v, %v", list, err)
	}
	changes, err := rt.SessionChanges(sid, ws)
	if err != nil {
		t.Fatalf("session changes: %v", err)
	}
	for _, change := range changes.Turns {
		if change.Turn == *rows[0].Index {
			t.Fatal("changes included an expired turn")
		}
	}
	if _, err := rt.SessionDiff(sid, ws, *rows[0].Index, "a.txt"); err == nil {
		t.Fatal("expired turn diff remained available")
	}
	if _, err := rt.RevertPreview(sid, ws, *rows[0].Index); err == nil {
		t.Fatal("old revert preview accepted")
	}
	if _, err := rt.RevertSession(sid, ws, *rows[0].Index); err == nil {
		t.Fatal("old revert accepted")
	}
	if _, err := rt.RevertPreview(other.SessionID, ws, 0); err != nil {
		t.Fatalf("other chat lost its snapshot: %v", err)
	}
	repo := rt.snapRepo(ws)
	if _, err := repo.Changed(context.Background(), oldTree, list[0].Tree); err == nil {
		t.Fatal("expired snapshot tree still stored")
	}
	res, err := rt.RevertSession(sid, ws, *rows[1].Index)
	if err != nil || !res.Snapshots {
		t.Fatalf("three-turn revert = %+v, %v", res, err)
	}
	if _, err := rt.RevertSession(sid, ws, *rows[0].Index); err == nil {
		t.Fatal("sequential revert crossed the retention boundary")
	}
}

func TestUndoLimitAppliesWithoutSnapshots(t *testing.T) {
	rt, ws, sid := undoRuntime(t, "", "", "", "")
	rt.snaps = nil
	for _, msg := range []string{"one", "two", "three", "four"} {
		turn(t, rt, sid, ws, msg)
	}
	rows := userRows(t, rt, sid)
	if _, err := rt.RevertSession(sid, ws, *rows[0].Index); err == nil {
		t.Fatal("old conversation-only revert accepted")
	}
	if _, err := rt.RevertSession(sid, ws, *rows[1].Index); err != nil {
		t.Fatalf("three-turn conversation revert: %v", err)
	}
	if _, err := rt.RevertSession(sid, ws, *rows[0].Index); err == nil {
		t.Fatal("sequential conversation revert crossed the retention boundary")
	}
}

func TestTurnHistoryExplainsUnavailableSnapshot(t *testing.T) {
	rt, ws, sid := undoRuntime(t, "", "")
	rt.snaps = nil
	turn(t, rt, sid, ws, "unprotected")
	preview, err := rt.RevertPreview(sid, ws, 0)
	if err != nil || preview.Snapshots || preview.Protection == nil || preview.Protection.Reason == "" {
		t.Fatalf("missing preview reason: %+v %v", preview, err)
	}
	history, err := rt.SessionHistory(sid, ws)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range history {
		if row.Role == "user" {
			if row.Protection == nil || row.Protection.State != "unavailable" || row.Protection.Reason == "" {
				t.Fatalf("missing protection reason: %+v", row)
			}
			return
		}
	}
	t.Fatal("missing user row")
}

func TestSnapshotFailureReportsReason(t *testing.T) {
	rt, ws, sid := undoRuntime(t, "")
	sess, err := rt.transcripts.Load(ws, sid)
	if err != nil {
		t.Fatal(err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	status := rt.recordTurnSnapshot(sess, home)
	if status.State != "unavailable" || !strings.Contains(status.Reason, "too broad") {
		t.Fatalf("missing failure reason: %+v", status)
	}
	entries, err := readProtection(sess.Dir)
	if err != nil || len(entries) != 1 || entries[0].Reason != status.Reason {
		t.Fatalf("failure not persisted: %+v %v", entries, err)
	}
}

func TestTurnHistoryShowsExclusionsAndExpiresAfterThreeTurns(t *testing.T) {
	rt, ws, sid := undoRuntime(t, "", "", "", "", "", "", "", "")
	if err := os.WriteFile(filepath.Join(ws, ".gitignore"), []byte("private.txt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "private.txt"), []byte("ignored"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "binary.dat"), []byte{'a', 0, 'b'}, 0o600); err != nil {
		t.Fatal(err)
	}
	turn(t, rt, sid, ws, "first")
	rows := userRows(t, rt, sid)
	p := rows[0].Protection
	if p == nil || p.State != "partial" || !slices.Contains(p.Excluded, "private.txt") || !slices.Contains(p.Excluded, "binary.dat") {
		t.Fatalf("missing exclusions: %+v", p)
	}
	turn(t, rt, sid, ws, "second")
	turn(t, rt, sid, ws, "third")
	turn(t, rt, sid, ws, "fourth")
	rows = userRows(t, rt, sid)
	if rows[0].Protection == nil || rows[0].Protection.State != "expired" {
		t.Fatalf("old turn not expired: %+v", rows[0])
	}
	sess, err := rt.transcripts.Load(ws, sid)
	if err != nil {
		t.Fatal(err)
	}
	status, err := readProtection(sess.Dir)
	if err != nil || len(status) != 3 {
		t.Fatalf("protection retention = %+v %v", status, err)
	}
}

func TestRevertPreviewShowsFilesExcludedAfterTurn(t *testing.T) {
	rt, ws, sid := undoRuntime(t, "", "")
	if err := os.WriteFile(filepath.Join(ws, ".gitignore"), []byte("later.env\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	turn(t, rt, sid, ws, "first")
	if err := os.WriteFile(filepath.Join(ws, "later.env"), []byte("ignored"), 0o600); err != nil {
		t.Fatal(err)
	}
	preview, err := rt.RevertPreview(sid, ws, 0)
	if err != nil || preview.Protection == nil || !slices.Contains(preview.Protection.Excluded, "later.env") {
		t.Fatalf("excluded path missing: %+v %v", preview, err)
	}
}

// scriptModel answers each request with the next round: a shell
// command becomes a tool call, "" a plain "finished" reply.
func scriptModel(t *testing.T, rounds ...string) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		mu.Lock()
		i := n
		n++
		mu.Unlock()
		if i < len(rounds) && rounds[i] != "" {
			args := fmt.Sprintf(`{"cmd":%q}`, rounds[i])
			if strings.HasPrefix(rounds[i], "{") {
				args = rounds[i]
			}
			fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", fmt.Sprintf(
				`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c%d","function":{"name":"shell","arguments":%q}}]}}]}`, i, args))
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"finished\"}}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	return srv
}

func undoRuntime(t *testing.T, rounds ...string) (*AgentRuntime, string, string) {
	t.Helper()
	if !snapshot.Supported() {
		t.Skip("git not available")
	}
	ws := t.TempDir()
	rt, _ := testRuntime(t)
	_ = rt.store.Keys.Set("openrouter", "sk-test")
	pointMockAt(rt, scriptModel(t, rounds...).URL)
	sess, err := rt.OpenSession("", ws)
	if err != nil {
		t.Fatal(err)
	}
	return rt, ws, sess.SessionID
}

func turn(t *testing.T, rt *AgentRuntime, sid, ws, msg string) {
	t.Helper()
	out, _ := chatToEnd(t, rt, agent.Request{SessionID: sid, Message: msg, Cwd: ws, Permissions: "full-access"})
	if out.Reply != "finished" {
		t.Fatalf("turn %q: %+v", msg, out)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return "<missing>"
	}
	return strings.TrimSpace(string(b))
}

func userRows(t *testing.T, rt *AgentRuntime, sid string) []session.HistoryMessage {
	t.Helper()
	h, err := rt.SessionHistory(sid, "")
	if err != nil {
		t.Fatal(err)
	}
	var out []session.HistoryMessage
	for _, m := range h {
		if m.Role == "user" {
			out = append(out, m)
		}
	}
	return out
}

// One snapshot per user turn is the whole undo model: reverting puts the
// folder back exactly as it was before the message, including deleting
// files the turn created.
func TestTurnSnapshotRestoresFolderExactly(t *testing.T) {
	rt, ws, sid := undoRuntime(t,
		"echo one > a.txt", "",
		"echo two > a.txt; echo c > c.txt", "")
	turn(t, rt, sid, ws, "first")
	turn(t, rt, sid, ws, "second")
	rows := userRows(t, rt, sid)
	if len(rows) != 2 || rows[1].Index == nil {
		t.Fatalf("rows %+v", rows)
	}
	second := *rows[1].Index

	pv, err := rt.RevertPreview(sid, "", second)
	if err != nil {
		t.Fatal(err)
	}
	if !pv.Snapshots || strings.Join(pv.Files, ",") != "a.txt" || strings.Join(pv.Deleted, ",") != "c.txt" || pv.Turns != 1 {
		t.Fatalf("preview %+v", pv)
	}

	res, err := rt.RevertSession(sid, "", second)
	if err != nil {
		t.Fatal(err)
	}
	if res.Message != "second" || strings.Join(res.Restored, ",") != "a.txt" || strings.Join(res.Deleted, ",") != "c.txt" {
		t.Fatalf("revert %+v", res)
	}
	if readFile(t, filepath.Join(ws, "a.txt")) != "one" || readFile(t, filepath.Join(ws, "c.txt")) != "<missing>" {
		t.Fatal("folder not restored to the snapshot")
	}
	if rows := userRows(t, rt, sid); len(rows) != 1 {
		t.Fatalf("reverted turn still visible: %+v", rows)
	}
	info, err := rt.OpenSession(sid, "")
	if err != nil {
		t.Fatal(err)
	}
	if info.Revert != nil {
		t.Fatal("revert left legacy pending metadata")
	}
}

// A revert ignores changes made after the turn, by the user or another
// session: the snapshot wins.
func TestRevertOverwritesLaterChanges(t *testing.T) {
	rt, ws, sid := undoRuntime(t, "echo agent > a.txt; echo agent > b.txt", "")
	if err := os.WriteFile(filepath.Join(ws, "a.txt"), []byte("orig\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	turn(t, rt, sid, ws, "edit")
	if err := os.WriteFile(filepath.Join(ws, "a.txt"), []byte("user\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := rt.RevertSession(sid, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.Restored, ",") != "a.txt" || strings.Join(res.Deleted, ",") != "b.txt" {
		t.Fatalf("revert %+v", res)
	}
	if readFile(t, filepath.Join(ws, "a.txt")) != "orig" || readFile(t, filepath.Join(ws, "b.txt")) != "<missing>" {
		t.Fatal("the snapshot did not win over later changes")
	}
}

func TestRecoverLatestUndoRestoresFilesAndConversation(t *testing.T) {
	rt, ws, sid := undoRuntime(t, "echo agent > a.txt; echo agent > b.txt", "")
	if err := os.WriteFile(filepath.Join(ws, "a.txt"), []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	turn(t, rt, sid, ws, "edit")
	if err := os.WriteFile(filepath.Join(ws, "a.txt"), []byte("later user work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.RevertSession(sid, ws, 0); err != nil {
		t.Fatal(err)
	}
	if readFile(t, filepath.Join(ws, "a.txt")) != "original" {
		t.Fatal("undo failed")
	}
	if ok, err := rt.RecoveryAvailable(sid, ws); err != nil || !ok {
		t.Fatalf("recovery missing: %v %v", ok, err)
	}
	if _, err := rt.RecoverLatest(sid, ws); err != nil {
		t.Fatal(err)
	}
	if readFile(t, filepath.Join(ws, "a.txt")) != "later user work" || readFile(t, filepath.Join(ws, "b.txt")) != "agent" {
		t.Fatal("files not recovered")
	}
	if rows := userRows(t, rt, sid); len(rows) != 1 {
		t.Fatalf("conversation not recovered: %+v", rows)
	} else if rows[0].Protection == nil || rows[0].Protection.State == "unknown" {
		t.Fatalf("snapshot protection not recovered: %+v", rows[0])
	}
}

func TestRecoveryIsPerSessionAndSurvivesSnapshotCollection(t *testing.T) {
	rt, ws, sid := undoRuntime(t, "echo agent > a.txt", "")
	turn(t, rt, sid, ws, "edit")
	other, err := rt.OpenSession("", ws)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.RevertSession(sid, ws, 0); err != nil {
		t.Fatal(err)
	}
	if ok, err := rt.RecoveryAvailable(other.SessionID, ws); err != nil || ok {
		t.Fatalf("recovery leaked to another chat: %v %v", ok, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), snapTimeout)
	defer cancel()
	if err := rt.collectSnapshots(ctx, rt.snapRepo(ws)); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.RecoverLatest(sid, ws); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(ws, "a.txt")); got != "agent" {
		t.Fatalf("recovery after collection: %q", got)
	}
}

func TestLatestUndoReplacesOnlyThatSessionsRecovery(t *testing.T) {
	rt, ws, sid := undoRuntime(t, "echo first > a.txt", "", "echo second > a.txt", "")
	turn(t, rt, sid, ws, "first")
	turn(t, rt, sid, ws, "second")
	rows := userRows(t, rt, sid)
	if _, err := rt.RevertSession(sid, ws, *rows[1].Index); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.RevertSession(sid, ws, *rows[0].Index); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.RecoverLatest(sid, ws); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(ws, "a.txt")); got != "first" {
		t.Fatalf("recovered wrong undo: %q", got)
	}
	if rows := userRows(t, rt, sid); len(rows) != 1 {
		t.Fatalf("recovered wrong conversation: %+v", rows)
	}
}

func TestRecoveryRestoresConversationWithoutFileSnapshots(t *testing.T) {
	rt, ws, sid := undoRuntime(t, "", "")
	rt.snaps = nil
	turn(t, rt, sid, ws, "hello")
	if _, err := rt.RevertSession(sid, ws, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.RecoverLatest(sid, ws); err != nil {
		t.Fatal(err)
	}
	if rows := userRows(t, rt, sid); len(rows) != 1 {
		t.Fatalf("conversation not recovered: %+v", rows)
	}
}

func TestRevertPreservesNewUncapturableFile(t *testing.T) {
	rt, ws, sid := undoRuntime(t, "", "")
	turn(t, rt, sid, ws, "hello")
	path := filepath.Join(ws, "new.dat")
	content := []byte{'a', 0, 'b'}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	preview, err := rt.RevertPreview(sid, ws, 0)
	if err != nil || !slices.Contains(preview.Skipped, "new.dat") {
		t.Fatalf("unsafe preview: %+v %v", preview, err)
	}
	if _, err := rt.RevertSession(sid, ws, 0); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || !slices.Equal(got, content) {
		t.Fatalf("uncapturable file lost: %v", err)
	}
}

func TestRevertDoesNotProceedIfRecoveryCannotBeSaved(t *testing.T) {
	rt, ws, sid := undoRuntime(t, "echo agent > a.txt", "")
	turn(t, rt, sid, ws, "edit")
	sess, err := rt.transcripts.Load(ws, sid)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(sess.Dir, recoveryFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.RevertSession(sid, ws, 0); err == nil {
		t.Fatal("undo accepted without a recovery snapshot")
	}
	if got := readFile(t, filepath.Join(ws, "a.txt")); got != "agent" {
		t.Fatalf("file changed: %q", got)
	}
	if rows := userRows(t, rt, sid); len(rows) != 1 {
		t.Fatalf("conversation changed: %+v", rows)
	}
}

func TestUndoAndRecoveryRejectStalePreviews(t *testing.T) {
	rt, ws, sid := undoRuntime(t, "echo agent > a.txt", "")
	turn(t, rt, sid, ws, "edit")
	preview, err := rt.RevertPreview(sid, ws, 0)
	if err != nil || preview.Version == "" {
		t.Fatalf("preview: %+v %v", preview, err)
	}
	if err := os.WriteFile(filepath.Join(ws, "later.txt"), []byte("later"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.RevertSessionChecked(sid, ws, 0, preview.Version); err == nil {
		t.Fatal("accepted stale undo preview")
	}
	if _, err := rt.RevertSession(sid, ws, 0); err != nil {
		t.Fatal(err)
	}
	recovery, err := rt.RecoveryPreview(sid, ws)
	if err != nil || recovery.Version == "" {
		t.Fatalf("recovery preview: %+v %v", recovery, err)
	}
	if err := os.WriteFile(filepath.Join(ws, "newer.txt"), []byte("newer"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.RecoverLatestChecked(sid, ws, recovery.Version); err == nil {
		t.Fatal("accepted stale recovery preview")
	}
	if got := readFile(t, filepath.Join(ws, "newer.txt")); got != "newer" {
		t.Fatalf("newer work lost: %q", got)
	}
}

func TestUndoRejectsPreviewAfterAnotherMessage(t *testing.T) {
	rt, ws, sid := undoRuntime(t, "", "", "", "")
	turn(t, rt, sid, ws, "first")
	preview, err := rt.RevertPreview(sid, ws, 0)
	if err != nil {
		t.Fatal(err)
	}
	turn(t, rt, sid, ws, "second")
	if _, err := rt.RevertSessionChecked(sid, ws, 0, preview.Version); err == nil {
		t.Fatal("accepted preview from before the second message")
	}
	if rows := userRows(t, rt, sid); len(rows) != 2 {
		t.Fatalf("new message lost: %+v", rows)
	}
}

func TestRecoveryRejectsCorruptConversationBeforeRestoringFiles(t *testing.T) {
	rt, ws, sid := undoRuntime(t, "echo agent > a.txt", "")
	turn(t, rt, sid, ws, "edit")
	if _, err := rt.RevertSession(sid, ws, 0); err != nil {
		t.Fatal(err)
	}
	sess, err := rt.transcripts.Load(ws, sid)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := readRecovery(sess.Dir)
	if err != nil {
		t.Fatal(err)
	}
	saved.Transcript = []byte("corrupt")
	data, err := json.Marshal(saved)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sess.Dir, recoveryFile), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.RecoverLatest(sid, ws); err == nil {
		t.Fatal("accepted corrupt recovery")
	}
	if got := readFile(t, filepath.Join(ws, "a.txt")); got != "<missing>" {
		t.Fatalf("file changed before recovery failed: %q", got)
	}
}

func TestRevertRejectsBadTargets(t *testing.T) {
	rt, ws, sid := undoRuntime(t, "")
	turn(t, rt, sid, ws, "hi")
	for _, idx := range []int{-1, 1, 99} {
		if _, err := rt.RevertSession(sid, "", idx); err == nil {
			t.Errorf("index %d accepted", idx)
		}
	}
}

func TestRevertRefusedWhileBusy(t *testing.T) {
	m := newBlockingModel(t)
	rt, _, _ := hubRuntime(t, m.server.URL)
	sid := openIn(t, rt)
	if _, _, err := rt.hub.Submit(agent.Request{SessionID: sid, Message: "x", Cwd: cwdOf(t, rt, sid)}, SubmitOpts{}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for m.inFlight.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := rt.RevertSession(sid, "", 0); err == nil || !strings.Contains(err.Error(), "running") {
		t.Fatalf("revert while busy: %v", err)
	}
}

func TestRevertConversationOnlyWithoutSnapshots(t *testing.T) {
	rt, ws, sid := undoRuntime(t, "echo one > a.txt", "")
	rt.snaps = nil
	turn(t, rt, sid, ws, "write")
	pv, err := rt.RevertPreview(sid, "", 0)
	if err != nil || pv.Snapshots {
		t.Fatalf("preview %+v %v", pv, err)
	}
	res, err := rt.RevertSession(sid, "", 0)
	if err != nil || len(res.Restored) != 0 || res.Snapshots {
		t.Fatalf("revert %+v %v", res, err)
	}
	if readFile(t, filepath.Join(ws, "a.txt")) != "one" {
		t.Fatal("files touched without snapshots")
	}
	if rows := userRows(t, rt, sid); len(rows) != 0 {
		t.Fatalf("conversation not reverted: %+v", rows)
	}
}

func TestForkAfterRevertCopiesTheRewoundConversation(t *testing.T) {
	rt, ws, sid := undoRuntime(t, "", "")
	turn(t, rt, sid, ws, "first")
	turn(t, rt, sid, ws, "second")
	rows := userRows(t, rt, sid)
	if _, err := rt.RevertSession(sid, "", *rows[1].Index); err != nil {
		t.Fatal(err)
	}
	fork, err := rt.ForkSession(sid, "")
	if err != nil {
		t.Fatal(err)
	}
	got := userRows(t, rt, fork.SessionID)
	if len(got) != 1 || got[0].Content != "first" {
		t.Fatalf("fork user rows = %+v", got)
	}
}

func TestSessionChangesAndDiffUseTurnSnapshots(t *testing.T) {
	rt, ws, sid := undoRuntime(t, "printf 'one\\n' > a.txt; printf 'new\\n' > b.txt", "", "printf 'two\\n' > a.txt", "")
	turn(t, rt, sid, ws, "create two files")
	turn(t, rt, sid, ws, "edit one file")
	changes, err := rt.SessionChanges(sid, "")
	if err != nil || !changes.Available || len(changes.Turns) != 2 || len(changes.Files) != 2 {
		t.Fatalf("changes = %+v, %v", changes, err)
	}
	if changes.Turns[0].Turn != 0 || len(changes.Turns[0].Files) != 2 || changes.Turns[0].Files[0].Status != "created" {
		t.Fatalf("first turn = %+v", changes.Turns[0])
	}
	if changes.Turns[1].Files[0].Status != "modified" || changes.Files[0].Status != "created" {
		t.Fatalf("edit and session summary = %+v", changes)
	}
	// Each turn names the tool calls it made, so the renderer can attach
	// a just-settled call to the running turn.
	if len(changes.Turns[0].CallIDs) == 0 || len(changes.Turns[1].CallIDs) == 0 {
		t.Fatalf("turn call ids = %+v", changes.Turns)
	}
	if slices.Contains(changes.Turns[0].CallIDs, changes.Turns[1].CallIDs[0]) {
		t.Fatalf("call id shared between turns: %+v", changes.Turns)
	}
	first, err := rt.SessionDiff(sid, "", 0, "a.txt")
	if err != nil || !strings.Contains(first.Patch, "+one") || !strings.Contains(first.Patch, "new file mode") {
		t.Fatalf("created file diff = %+v, %v", first, err)
	}
	second, err := rt.SessionDiff(sid, "", changes.Turns[1].Turn, "a.txt")
	if err != nil || !strings.Contains(second.Patch, "-one") || !strings.Contains(second.Patch, "+two") {
		t.Fatalf("edited file diff = %+v, %v", second, err)
	}
	if _, err := rt.SessionDiff(sid, "", -1, "../outside"); err == nil {
		t.Fatal("diff accepted path outside workspace")
	}
	if _, err := rt.SessionDiff(sid, "", -1, "not-changed.txt"); err == nil {
		t.Fatal("diff accepted path not changed by this session")
	}
	if _, err := rt.RevertSession(sid, "", changes.Turns[1].Turn); err != nil {
		t.Fatal(err)
	}
	hidden, err := rt.SessionChanges(sid, "")
	if err != nil || len(hidden.Turns) != 1 {
		t.Fatalf("reverted turn still visible: %+v, %v", hidden, err)
	}
	if _, err := rt.SessionDiff(sid, "", changes.Turns[1].Turn, "a.txt"); err == nil {
		t.Fatal("reverted turn diff remained visible")
	}
}

func TestReviewSessionCarriesTheSessionsOwnDiff(t *testing.T) {
	rt, ws, sid := undoRuntime(t, "printf 'cost $1\\n' > a.txt", "")
	turn(t, rt, sid, ws, "create a file")
	cmds := runtimeCommands{rt}
	got, err := cmds.LoadCommand(ws, sid, "review", "session")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Prompt, "```diff") || !strings.Contains(got.Prompt, "+cost $1") || strings.Contains(got.Prompt, "$SESSION_CHANGES") {
		t.Fatalf("session review lacks the diff (or mangled $1): %s", got.Prompt)
	}
	if strings.Join(got.Tools, ",") != "report_findings" || got.Mode != "plan" {
		t.Fatalf("review command: %+v", got)
	}
	plain, err := cmds.LoadCommand(ws, sid, "review", "main")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plain.Prompt, "```diff") || strings.Contains(plain.Prompt, "$SESSION_CHANGES") {
		t.Fatalf("a branch review carries no session diff: %s", plain.Prompt)
	}
}

// /review session never widens to other changes: without snapshots, or
// with nothing changed, the command stops before the model runs.
func TestReviewSessionStopsWhenItHasNoSessionChanges(t *testing.T) {
	rt, ws, sid := undoRuntime(t, "true", "")
	cmds := runtimeCommands{rt}
	snaps := rt.snaps
	rt.snaps = nil // snapshots off (e.g. no git)
	_, err := cmds.LoadCommand(ws, sid, "review", "session")
	if err == nil || !strings.Contains(err.Error(), "session changes unavailable") || !strings.Contains(err.Error(), "/review") {
		t.Fatalf("no snapshots: %v", err)
	}
	rt.snaps = snaps
	turn(t, rt, sid, ws, "change nothing")
	_, err = cmds.LoadCommand(ws, sid, "review", "session")
	if err == nil || !strings.Contains(err.Error(), "has not changed any files") {
		t.Fatalf("nothing changed: %v", err)
	}
	if _, err := cmds.LoadCommand(ws, sid, "review", ""); err != nil {
		t.Fatalf("a plain review still works: %v", err)
	}
}

func TestFileExcerptShowsLinesInsideTheWorkspaceOnly(t *testing.T) {
	rt, ws, sid := undoRuntime(t, "true", "")
	turn(t, rt, sid, ws, "noop")
	var b strings.Builder
	for i := 1; i <= 40; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	os.WriteFile(filepath.Join(ws, "a.go"), []byte(b.String()), 0o644)
	ex, err := rt.FileExcerpt(sid, "", filepath.Join(ws, "a.go"), 10, 12)
	if err != nil {
		t.Fatal(err)
	}
	if ex.StartLine != 7 || len(ex.Lines) != 9 || ex.Lines[0] != "line 7" || ex.Lines[8] != "line 15" || ex.Rel != "a.go" {
		t.Fatalf("three lines of context around 10-12: %+v", ex)
	}
	if ex, _ := rt.FileExcerpt(sid, "", filepath.Join(ws, "a.go"), 39, 400); ex.Lines[len(ex.Lines)-1] != "line 40" {
		t.Fatalf("clamped to the end: %+v", ex)
	}
	outside := filepath.Join(t.TempDir(), "secret.txt")
	os.WriteFile(outside, []byte("secret\n"), 0o644)
	if _, err := rt.FileExcerpt(sid, "", outside, 1, 1); err == nil {
		t.Fatal("files outside the session workspace are refused")
	}
	os.WriteFile(filepath.Join(ws, "bin.dat"), []byte{0, 1, 2}, 0o644)
	if _, err := rt.FileExcerpt(sid, "", filepath.Join(ws, "bin.dat"), 1, 1); err == nil {
		t.Fatal("binary files are refused")
	}
}
