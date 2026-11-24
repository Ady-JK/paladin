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
	"github.com/Ady-JK/paladin/core/internal/permission"
)

func shellRounds(t *testing.T, cmds ...string) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		mu.Lock()
		i := n
		n++
		mu.Unlock()
		if i < len(cmds) {
			args := fmt.Sprintf(`{"reason":"test","cmd":%q}`, cmds[i])
			fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", fmt.Sprintf(
				`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c%d","function":{"name":"shell","arguments":%q}}]}}]}`, i, args))
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"finished\"}}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	return srv
}

func waitApproval(t *testing.T, rt *AgentRuntime, sid, id string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !rt.broker.Pending(sid, id) {
		if time.Now().After(deadline) {
			t.Fatalf("call %s never asked for approval", id)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestFullAccessNeverAsks(t *testing.T) {
	ws := t.TempDir()
	srv := shellRounds(t, "make -v >/dev/null 2>&1; echo ok")
	rt, _ := testRuntime(t)
	_ = rt.store.Keys.Set("openrouter", "sk-test")
	pointMockAt(rt, srv.URL)
	sess, _ := rt.OpenSession("", ws)
	out, chunks := chatToEnd(t, rt, agent.Request{SessionID: sess.SessionID, Message: "go", Cwd: ws, Permissions: "full-access"})
	if out.Reply != "finished" {
		t.Fatalf("outcome %+v", out)
	}
	for _, c := range chunks {
		if c.Approval != nil {
			t.Fatalf("full access asked: %+v", c.Approval)
		}
	}
}

func TestBadPresetRejected(t *testing.T) {
	rt, _ := testRuntime(t)
	ws := t.TempDir()
	sess, _ := rt.OpenSession("", ws)
	if _, _, err := rt.hub.Submit(agent.Request{SessionID: sess.SessionID, Message: "x", Cwd: ws, Permissions: "yolo"}, SubmitOpts{}); err == nil {
		t.Fatal("bad preset accepted")
	}
}

func TestPermissionsViewReportsFullAccessOnly(t *testing.T) {
	rt, _ := testRuntime(t)
	ws := t.TempDir()
	if _, err := rt.PermissionsSet("global", ""); err != nil {
		t.Fatal(err)
	}
	v, err := rt.PermissionsGet(ws)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Global)+len(v.Folder)+len(v.Session)+len(v.Folders) != 0 || len(v.Presets) != 1 || v.Presets[0] != "full-access" {
		t.Fatalf("view %+v", v)
	}
}

// An everyday command runs, and a flagged one waits for the user: once
// approved it runs, denied it never does.
func TestGuardAsksBeforeAFlaggedCommandOnly(t *testing.T) {
	for _, tc := range []struct {
		name, reply string
		ran         bool
	}{{"deny", "deny", false}, {"approve", "once", true}} {
		t.Run(tc.name, func(t *testing.T) {
			ws := t.TempDir()
			marker := filepath.Join(ws, "ran.txt")
			// The flagged part (a tunnel binary that does not exist) fails harmlessly;
			// the marker proves whether the command line ran at all.
			cmd := "touch ran.txt; ngrok http 3000"
			srv := shellRounds(t, cmd)
			rt, _ := testRuntime(t)
			_ = rt.store.Keys.Set("openrouter", "sk-test")
			pointMockAt(rt, srv.URL)
			sess, _ := rt.OpenSession("", ws)
			done := make(chan []agent.ChatChunk, 1)
			go func() {
				_, chunks := chatToEnd(t, rt, agent.Request{SessionID: sess.SessionID, Message: "go", Cwd: ws})
				done <- chunks
			}()
			waitApproval(t, rt, sess.SessionID, "c0")
			if _, err := rt.AnswerApproval(sess.SessionID, "c0", tc.reply); err != nil {
				t.Fatal(err)
			}
			var chunks []agent.ChatChunk
			select {
			case chunks = <-done:
			case <-time.After(20 * time.Second):
				t.Fatal("turn never finished")
			}
			asked := 0
			for _, c := range chunks {
				if c.Approval != nil && c.Approval.State == permission.StatePending {
					asked++
					if strings.Join(c.Approval.Choices, ",") != "once,deny" {
						t.Fatalf("choices %v", c.Approval.Choices)
					}
				}
			}
			if asked != 1 {
				t.Fatalf("asked %d times", asked)
			}
			if _, err := os.Stat(marker); (err == nil) != tc.ran {
				t.Fatalf("ran = %v, want %v", err == nil, tc.ran)
			}
		})
	}
}
