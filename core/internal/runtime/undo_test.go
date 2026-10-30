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

func TestStepsRecordedForFileChangingCalls(t *testing.T) {
	rt, ws, sid := undoRuntime(t, "echo one > a.txt", "")
	turn(t, rt, sid, ws, "write a")
	steps, err := rt.loadSteps(sid, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 || strings.Join(steps[0].Files, ",") != "a.txt" || steps[0].Turn != 0 || steps[0].Before == steps[0].After {
		t.Fatalf("steps %+v", steps)
	}
	if rows := userRows(t, rt, sid); len(rows) != 1 || rows[0].Index == nil || *rows[0].Index != 0 {
		t.Fatalf("user rows %+v", rows)
	}
}

func TestRevertRedoAndCommitOnNextTurn(t *testing.T) {
	rt, ws, sid := undoRuntime(t,
		"echo one > a.txt", "",
		"echo two > a.txt; mkdir -p sub; echo new > sub/b.txt", "",
		"echo three > c.txt", "")
	os.WriteFile(filepath.Join(ws, "a.txt"), []byte("orig\n"), 0o644)
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
	if !pv.Snapshots || strings.Join(pv.Files, ",") != "a.txt,sub/b.txt" || len(pv.Conflicts) != 0 || pv.Turns != 1 {
		t.Fatalf("preview %+v", pv)
	}

	res, err := rt.RevertSession(sid, "", second)
	if err != nil {
		t.Fatal(err)
	}
	if res.Message != "second" || strings.Join(res.Restored, ",") != "a.txt,sub/b.txt" {
		t.Fatalf("revert %+v", res)
	}
	if readFile(t, filepath.Join(ws, "a.txt")) != "one" || readFile(t, filepath.Join(ws, "sub/b.txt")) != "<missing>" {
		t.Fatal("files not restored")
	}
	if _, err := os.Stat(filepath.Join(ws, "sub")); !os.IsNotExist(err) {
		t.Fatal("empty dir left behind")
	}
	if rows := userRows(t, rt, sid); len(rows) != 1 {
		t.Fatalf("reverted turn still visible: %+v", rows)
	}
	if info, _ := rt.OpenSession(sid, ""); info.Revert == nil || info.Revert.Index != second {
		t.Fatalf("session info lacks revert: %+v", info.Revert)
	}

	if _, err := rt.UnrevertSession(sid, ""); err != nil {
		t.Fatal(err)
	}
	if readFile(t, filepath.Join(ws, "a.txt")) != "two" || readFile(t, filepath.Join(ws, "sub/b.txt")) != "new" {
		t.Fatal("redo did not bring files back")
	}
	if rows := userRows(t, rt, sid); len(rows) != 2 {
		t.Fatalf("redo did not restore conversation: %+v", rows)
	}

	// Reverting the first turn while nothing is pending, then sending a
	// new message, makes the revert permanent.
	if _, err := rt.RevertSession(sid, "", *rows[0].Index); err != nil {
		t.Fatal(err)
	}
	if readFile(t, filepath.Join(ws, "a.txt")) != "orig" {
		t.Fatalf("a.txt = %q", readFile(t, filepath.Join(ws, "a.txt")))
	}
	turn(t, rt, sid, ws, "third")
	rows = userRows(t, rt, sid)
	if len(rows) != 1 || rows[0].Content != "third" || *rows[0].Index != 0 {
		t.Fatalf("after commit %+v", rows)
	}
	if info, _ := rt.OpenSession(sid, ""); info.Revert != nil {
		t.Fatal("revert still pending after a new turn")
	}
	steps, _ := rt.loadSteps(sid, "")
	if len(steps) != 1 || steps[0].Files[0] != "c.txt" {
		t.Fatalf("old steps survived the commit: %+v", steps)
	}
	if _, err := rt.UnrevertSession(sid, ""); err == nil {
		t.Fatal("redo after commit should fail")
	}
}

func TestRevertSkipsFilesChangedSince(t *testing.T) {
	rt, ws, sid := undoRuntime(t, "echo agent > a.txt; echo agent > b.txt", "")
	turn(t, rt, sid, ws, "edit")
	os.WriteFile(filepath.Join(ws, "a.txt"), []byte("user\n"), 0o644)
	pv, err := rt.RevertPreview(sid, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(pv.Conflicts, ",") != "a.txt" {
		t.Fatalf("preview %+v", pv)
	}
	res, err := rt.RevertSession(sid, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.Skipped, ",") != "a.txt" || strings.Join(res.Restored, ",") != "b.txt" {
		t.Fatalf("revert %+v", res)
	}
	if readFile(t, filepath.Join(ws, "a.txt")) != "user" || readFile(t, filepath.Join(ws, "b.txt")) != "<missing>" {
		t.Fatal("wrong files touched")
	}
	// Redo leaves a file alone if it changed after the revert.
	os.WriteFile(filepath.Join(ws, "b.txt"), []byte("mine\n"), 0o644)
	res, err = rt.UnrevertSession(sid, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.Skipped, ",") != "b.txt" || readFile(t, filepath.Join(ws, "b.txt")) != "mine" {
		t.Fatalf("redo %+v", res)
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
	if _, err := rt.UnrevertSession(sid, ""); err == nil {
		t.Error("redo with nothing reverted accepted")
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

func TestRevertSkipsFilesWhoseOriginalWasNeverCaptured(t *testing.T) {
	rt, ws, sid := undoRuntime(t, "echo small > big.txt", "")
	big := strings.Repeat("x", 10<<20+1)
	if err := os.WriteFile(filepath.Join(ws, "big.txt"), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	turn(t, rt, sid, ws, "shrink it")
	pv, err := rt.RevertPreview(sid, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(pv.Conflicts, ",") != "big.txt" || len(pv.Files) != 0 {
		t.Fatalf("preview %+v", pv)
	}
	res, err := rt.RevertSession(sid, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.Skipped, ",") != "big.txt" || readFile(t, filepath.Join(ws, "big.txt")) != "small" {
		t.Fatalf("revert %+v", res)
	}
}

func TestForkDuringAPendingRevertCopiesOnlyTheVisibleConversation(t *testing.T) {
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

func (r *AgentRuntime) loadSteps(sessionID, cwd string) ([]Step, error) {
	sess, err := r.transcripts.Load(cwd, sessionID)
	if err != nil {
		return nil, err
	}
	return readSteps(sess.Dir)
}
