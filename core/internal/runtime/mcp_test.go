package runtime

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Ady-JK/paladin/core/internal/agent"
	"github.com/Ady-JK/paladin/core/internal/mcp"
	"github.com/Ady-JK/paladin/core/internal/mcp/mcptest"
	"github.com/Ady-JK/paladin/core/internal/permission"
)

// toolModel asks for each call in turn, then answers "finished". It
// keeps every request body so tests can inspect tools and results.
type toolModel struct {
	srv    *httptest.Server
	mu     sync.Mutex
	bodies []string
}

func newToolModel(t *testing.T, calls ...[2]string) *toolModel {
	t.Helper()
	m := &toolModel{}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		m.mu.Lock()
		i := len(m.bodies)
		m.bodies = append(m.bodies, string(body))
		m.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		if i < len(calls) {
			fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", fmt.Sprintf(
				`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c%d","function":{"name":%q,"arguments":%q}}]}}]}`, i, calls[i][0], calls[i][1]))
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"finished\"}}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(m.srv.Close)
	return m
}

func (m *toolModel) body(i int) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if i >= len(m.bodies) {
		return ""
	}
	return m.bodies[i]
}

func mcpRuntime(t *testing.T, url string) (*AgentRuntime, *[]mcp.ServerStatus, *sync.Mutex) {
	t.Helper()
	rt, _ := testRuntime(t)
	if err := rt.store.Keys.Set("openrouter", "sk-test"); err != nil {
		t.Fatal(err)
	}
	pointMockAt(rt, url)
	var mu sync.Mutex
	var events []mcp.ServerStatus
	rt.SetMCPSink(func(s mcp.ServerStatus) {
		mu.Lock()
		events = append(events, s)
		mu.Unlock()
	})
	t.Cleanup(func() { rt.ShutdownTurns(3 * time.Second) })
	return rt, &events, &mu
}

func dummyConfig(t *testing.T) string {
	return fmt.Sprintf(`{"mcpServers":{"dummy":{"command":%q}}}`, mcptest.Dummy(t))
}

func TestTurnUsesMCPTools(t *testing.T) {
	model := newToolModel(t, [2]string{"mcp__dummy__echo", `{"text":"hello from mcp"}`})
	rt, events, mu := mcpRuntime(t, model.srv.URL)
	if _, err := rt.MCPSetConfig(mcp.ScopeGlobal, "", dummyConfig(t)); err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	sess, _ := rt.OpenSession("", ws)
	// readOnlyHint is not trusted; an allow rule is what lets it run.
	if err := rt.perms.Add(permission.ScopeGlobal, ws, sess.SessionID, permission.Rule{Tool: permission.ToolMCP, Pattern: "dummy/echo", Action: permission.ActionAllow}); err != nil {
		t.Fatal(err)
	}
	out, chunks := chatToEnd(t, rt, agent.Request{SessionID: sess.SessionID, Message: "go", Cwd: ws})
	if out.Reply != "finished" {
		t.Fatalf("outcome %+v", out)
	}
	first := model.body(0)
	if !strings.Contains(first, `"mcp__dummy__echo"`) || !strings.Contains(first, "Dummy server for Paladin tests") {
		t.Fatal("first request lacks the MCP tools or server instructions")
	}
	if !strings.Contains(model.body(1), "hello from mcp") {
		t.Fatalf("tool result not sent back: %s", model.body(1))
	}
	for _, c := range chunks {
		if c.Approval != nil {
			t.Fatalf("allowed MCP tool asked: %+v", c.Approval)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	ready := false
	for _, e := range *events {
		ready = ready || (e.Name == "dummy" && e.State == mcp.StateReady)
	}
	if !ready {
		t.Fatalf("no ready status event: %+v", *events)
	}
}

func TestMCPToolNeedsApproval(t *testing.T) {
	model := newToolModel(t, [2]string{"mcp__dummy__slow", `{"ms":1}`})
	rt, _, _ := mcpRuntime(t, model.srv.URL)
	if _, err := rt.MCPSetConfig(mcp.ScopeGlobal, "", dummyConfig(t)); err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	sess, _ := rt.OpenSession("", ws)
	done := make(chan agent.ChatOutcome, 1)
	go func() {
		o, _ := chatToEnd(t, rt, agent.Request{SessionID: sess.SessionID, Message: "go", Cwd: ws})
		done <- o
	}()
	waitApproval(t, rt, sess.SessionID, "c0")
	if _, err := rt.AnswerApproval(sess.SessionID, "c0", "folder"); err != nil {
		t.Fatal(err)
	}
	select {
	case o := <-done:
		if o.Reply != "finished" {
			t.Fatalf("%+v", o)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("turn never finished")
	}
	if !strings.Contains(model.body(1), "slept") {
		t.Fatalf("slow result missing: %s", model.body(1))
	}
	rules, _ := rt.perms.Rules("folder", ws, sess.SessionID)
	if len(rules) != 1 || rules[0].Pattern != "dummy/slow" || rules[0].Tool != "mcp" {
		t.Fatalf("rules %+v", rules)
	}
}

// Plan mode shows every MCP tool but asks once/deny for each call, even
// for a tool the server calls read-only and an allow rule covers.
func TestPlanModeAsksForEveryMCPCall(t *testing.T) {
	model := newToolModel(t, [2]string{"mcp__dummy__echo", `{"text":"planned"}`})
	rt, _, _ := mcpRuntime(t, model.srv.URL)
	if _, err := rt.MCPSetConfig(mcp.ScopeGlobal, "", dummyConfig(t)); err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	sess, _ := rt.OpenSession("", ws)
	if err := rt.perms.Add(permission.ScopeGlobal, ws, sess.SessionID, permission.Rule{Tool: permission.ToolMCP, Pattern: "dummy", Action: permission.ActionAllow}); err != nil {
		t.Fatal(err)
	}
	done := make(chan []agent.ChatChunk, 1)
	go func() {
		_, chunks := chatToEnd(t, rt, agent.Request{SessionID: sess.SessionID, Message: "plan", Cwd: ws, Mode: "plan"})
		done <- chunks
	}()
	waitApproval(t, rt, sess.SessionID, "c0")
	if _, err := rt.AnswerApproval(sess.SessionID, "c0", "once"); err != nil {
		t.Fatal(err)
	}
	var chunks []agent.ChatChunk
	select {
	case chunks = <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("turn never finished")
	}
	var card *permission.Approval
	for _, c := range chunks {
		if c.Approval != nil && card == nil {
			card = c.Approval
		}
	}
	if card == nil || card.Server != "dummy" || card.MCPTool != "echo" || strings.Join(card.Choices, ",") != "once,deny" {
		t.Fatalf("card %+v", card)
	}
	if !strings.Contains(model.body(1), "planned") {
		t.Fatalf("echo result missing: %s", model.body(1))
	}
	var req struct {
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
	}
	if err := json.Unmarshal([]byte(model.body(0)), &req); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tl := range req.Tools {
		names[tl.Function.Name] = true
	}
	if !names["mcp__dummy__echo"] || !names["mcp__dummy__slow"] || names["write"] {
		t.Fatalf("plan tools %v", names)
	}
}

func TestMCPFolderTrustFlow(t *testing.T) {
	rt, _, _ := mcpRuntime(t, "http://127.0.0.1:1")
	ws := t.TempDir()
	if err := mcp.WriteFile(mcp.FolderPath(ws), []byte(dummyConfig(t))); err != nil {
		t.Fatal(err)
	}
	ov := rt.MCPList(ws)
	if ov.Trusted || !ov.HasFolderConfig || len(ov.Servers) != 1 || ov.Servers[0].State != mcp.StateUntrusted {
		t.Fatalf("%+v", ov)
	}
	ov, err := rt.MCPTrust(ws, true)
	if err != nil || !ov.Trusted || ov.FolderPath != mcp.FolderPath(ws) {
		t.Fatalf("%+v %v", ov, err)
	}
	if _, err := rt.MCPRestart(mcp.ScopeFolder, ws, "dummy"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if s := rt.MCPList(ws).Servers; len(s) == 1 && s[0].State == mcp.StateReady {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if s := rt.MCPList(ws).Servers; s[0].State != mcp.StateReady {
		t.Fatalf("%+v", s)
	}
	if ov, err := rt.MCPSetEnabled(mcp.ScopeFolder, ws, "dummy", false); err != nil || ov.Servers[0].State != mcp.StateDisabled || !ov.Trusted {
		t.Fatalf("%+v %v", ov, err)
	}
	cfg, err := rt.MCPConfig(mcp.ScopeFolder, ws)
	if err != nil || !cfg.Exists || !strings.Contains(cfg.Text, `"enabled": false`) {
		t.Fatalf("%+v %v", cfg, err)
	}
}
