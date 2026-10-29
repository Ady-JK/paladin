package runtime

import (
	"fmt"
	"net/http"
	"net/http/httptest"
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
			args := fmt.Sprintf(`{"cmd":%q}`, cmds[i])
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

func TestApprovalRoundTripAndSessionRule(t *testing.T) {
	ws := t.TempDir()
	srv := shellRounds(t, "echo hi | tr a-z A-Z > out.txt; make -v >/dev/null 2>&1; echo made", "make -v >/dev/null 2>&1; echo again")
	rt, _ := testRuntime(t)
	if err := rt.store.Keys.Set("openrouter", "sk-test"); err != nil {
		t.Fatal(err)
	}
	pointMockAt(rt, srv.URL)
	sess, err := rt.OpenSession("", ws)
	if err != nil {
		t.Fatal(err)
	}
	type res struct {
		out    agent.ChatOutcome
		chunks []agent.ChatChunk
	}
	done := make(chan res, 1)
	go func() {
		o, c := chatToEnd(t, rt, agent.Request{SessionID: sess.SessionID, Message: "go", Cwd: ws})
		done <- res{o, c}
	}()
	waitApproval(t, rt, sess.SessionID, "c0")
	if _, err := rt.AnswerApproval(sess.SessionID, "nope", "once"); err == nil {
		t.Fatal("unknown approval accepted")
	}
	if _, err := rt.AnswerApproval(sess.SessionID, "c0", "maybe"); err == nil {
		t.Fatal("bad decision accepted")
	}
	if ok, err := rt.AnswerApproval(sess.SessionID, "c0", "session"); !ok || err != nil {
		t.Fatalf("answer: %v %v", ok, err)
	}
	var r res
	select {
	case r = <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("turn never finished; second call probably re-asked")
	}
	if r.out.Reply != "finished" {
		t.Fatalf("outcome %+v", r.out)
	}
	var states []string
	for _, c := range r.chunks {
		if c.Approval != nil {
			states = append(states, c.Approval.ID+":"+c.Approval.State)
		}
	}
	if strings.Join(states, ",") != "c0:pending,c0:approved" {
		t.Fatalf("approval chunks %v", states)
	}
	rules, _ := rt.perms.Rules("session", ws, sess.SessionID)
	if len(rules) == 0 {
		t.Fatal("session rule not remembered")
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

func TestPermissionsGetSet(t *testing.T) {
	rt, _ := testRuntime(t)
	ws := t.TempDir()
	if _, err := rt.PermissionsSet("global", "", []permission.Rule{{Tool: "shell", Pattern: "npm test", Action: "allow"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.PermissionsSet("folder", ws, []permission.Rule{{Tool: "shell", Pattern: "git push", Action: "deny"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.PermissionsSet("folder", ws, []permission.Rule{{Tool: "write", Pattern: "relative", Action: "allow"}}); err == nil {
		t.Fatal("relative write rule accepted")
	}
	if _, err := rt.PermissionsSet("session", ws, nil); err == nil {
		t.Fatal("session scope must not be settable")
	}
	v, err := rt.PermissionsGet(ws)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Global) != 1 || len(v.Folder) != 1 || v.Folder[0].Pattern != "git push" || len(v.Folders) != 1 {
		t.Fatalf("view %+v", v)
	}
	if len(v.Presets) != 3 || v.Sandbox.Kind == "x" {
		t.Fatalf("presets %+v", v.Presets)
	}
}
