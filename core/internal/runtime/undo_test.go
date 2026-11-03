package runtime

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Ady-JK/paladin/core/internal/agent"
	"github.com/Ady-JK/paladin/core/internal/session"
	"github.com/Ady-JK/paladin/core/internal/snapshot"
)

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
		t.Fatal("a revert is final and leaves no pending undo")
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
