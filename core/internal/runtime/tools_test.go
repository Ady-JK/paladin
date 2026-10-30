package runtime

import (
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/agent"
	"github.com/Ady-JK/paladin/core/internal/mcp"
)

func boolp(b bool) *bool { return &b }

func sentTool(body, name string) bool { return strings.Contains(body, `"name":"`+name+`"`) }

func findTool(t *testing.T, ov ToolsOverview, name string) ToolInfo {
	t.Helper()
	for _, ti := range ov.Tools {
		if ti.Name == name {
			return ti
		}
	}
	t.Fatalf("no tool %q in %+v", name, ov.Tools)
	return ToolInfo{}
}

func TestToolSwitchesSessionBeatsGlobal(t *testing.T) {
	model := newToolModel(t)
	rt, _, _ := mcpRuntime(t, model.srv.URL)
	ws := t.TempDir()
	sess, _ := rt.OpenSession("", ws)
	other, _ := rt.OpenSession("", ws)

	if _, err := rt.ToolsSet("global", ws, "", []string{"shell"}, boolp(false)); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.ToolsSet("session", ws, sess.SessionID, []string{"shell"}, boolp(true)); err != nil {
		t.Fatal(err)
	}
	ov, err := rt.ToolsList(ws, sess.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	shell := findTool(t, ov, "shell")
	if !shell.Enabled || shell.Global == nil || *shell.Global || shell.Session == nil || !*shell.Session || shell.Source != "builtin" {
		t.Fatalf("%+v", shell)
	}

	chatToEnd(t, rt, agent.Request{SessionID: sess.SessionID, Message: "go", Cwd: ws})
	if !sentTool(model.body(0), "shell") {
		t.Fatal("session switch should turn shell back on")
	}
	chatToEnd(t, rt, agent.Request{SessionID: other.SessionID, Message: "go", Cwd: ws})
	body := model.body(1)
	if sentTool(body, "shell") || !sentTool(body, "read") {
		t.Fatal("global switch should drop shell only")
	}
	if !strings.Contains(body, "switched off") {
		t.Fatal("the prompt should say which built-in tools are off")
	}

	// Clearing the session switch falls back to global.
	if _, err := rt.ToolsSet("session", ws, sess.SessionID, []string{"shell"}, nil); err != nil {
		t.Fatal(err)
	}
	ov, _ = rt.ToolsList(ws, sess.SessionID)
	if s := findTool(t, ov, "shell"); s.Enabled || s.Session != nil {
		t.Fatalf("%+v", s)
	}
	if _, err := rt.ToolsSet("session", ws, "", []string{"shell"}, boolp(true)); err == nil {
		t.Fatal("a session switch needs a session")
	}
	if _, err := rt.ToolsSet("galaxy", ws, "", []string{"shell"}, boolp(true)); err == nil {
		t.Fatal("unknown scope")
	}
}

func TestNewMCPToolsWaitForTheUserInAStartedSession(t *testing.T) {
	model := newToolModel(t)
	rt, _, _ := mcpRuntime(t, model.srv.URL)
	var mu sync.Mutex
	var events []ToolsEvent
	rt.SetToolsSink(func(e ToolsEvent) { mu.Lock(); events = append(events, e); mu.Unlock() })
	ws := t.TempDir()
	old, _ := rt.OpenSession("", ws)
	chatToEnd(t, rt, agent.Request{SessionID: old.SessionID, Message: "first", Cwd: ws})

	if _, err := rt.MCPSetConfig(mcp.ScopeGlobal, "", dummyConfig(t)); err != nil {
		t.Fatal(err)
	}
	chatToEnd(t, rt, agent.Request{SessionID: old.SessionID, Message: "second", Cwd: ws})
	if sentTool(model.body(1), "mcp__dummy__echo") {
		t.Fatal("new MCP tools must wait for the user in a started session")
	}
	mu.Lock()
	if len(events) == 0 || events[0].Event != "tools/pending" || events[0].SessionID != old.SessionID ||
		!contains(events[0].Pending, "mcp__dummy__echo") {
		t.Fatalf("events %+v", events)
	}
	mu.Unlock()
	ov, _ := rt.ToolsList(ws, old.SessionID)
	if echo := findTool(t, ov, "mcp__dummy__echo"); !echo.Pending || echo.Source != "mcp" || echo.Server != "dummy" || echo.Tool != "echo" || !echo.ReadOnly {
		t.Fatalf("%+v", echo)
	}
	if !contains(ov.Pending, "mcp__dummy__echo") {
		t.Fatalf("pending %v", ov.Pending)
	}

	// A new session takes every tool straight away.
	fresh, _ := rt.OpenSession("", ws)
	if ov, _ := rt.ToolsList(ws, fresh.SessionID); len(ov.Pending) != 0 {
		t.Fatalf("fresh session pending %v", ov.Pending)
	}
	chatToEnd(t, rt, agent.Request{SessionID: fresh.SessionID, Message: "go", Cwd: ws})
	if !sentTool(model.body(2), "mcp__dummy__echo") {
		t.Fatal("fresh session should get MCP tools")
	}

	ov, err := rt.ToolsAccept(ws, old.SessionID, nil)
	if err != nil || len(ov.Pending) != 0 {
		t.Fatalf("accept %v %v", ov.Pending, err)
	}
	chatToEnd(t, rt, agent.Request{SessionID: old.SessionID, Message: "third", Cwd: ws})
	if !sentTool(model.body(3), "mcp__dummy__echo") {
		t.Fatal("accepted tools join the next turn")
	}
}

func TestAcceptSomePendingTools(t *testing.T) {
	model := newToolModel(t)
	rt, _, _ := mcpRuntime(t, model.srv.URL)
	ws := t.TempDir()
	sess, _ := rt.OpenSession("", ws)
	chatToEnd(t, rt, agent.Request{SessionID: sess.SessionID, Message: "first", Cwd: ws})
	if _, err := rt.MCPSetConfig(mcp.ScopeGlobal, "", dummyConfig(t)); err != nil {
		t.Fatal(err)
	}
	chatToEnd(t, rt, agent.Request{SessionID: sess.SessionID, Message: "second", Cwd: ws})
	ov, err := rt.ToolsAccept(ws, sess.SessionID, []string{"mcp__dummy__echo"})
	if err != nil {
		t.Fatal(err)
	}
	if contains(ov.Pending, "mcp__dummy__echo") || !contains(ov.Pending, "mcp__dummy__slow") {
		t.Fatalf("pending %v", ov.Pending)
	}
	if _, err := rt.ToolsAccept(ws, "", nil); err == nil {
		t.Fatal("accept needs a session")
	}
}

func TestToolsListWithoutSession(t *testing.T) {
	rt, _ := testRuntime(t)
	ov, err := rt.ToolsList("", "")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, ti := range ov.Tools {
		names = append(names, ti.Name)
		if ti.Description == "" || !ti.Enabled || ti.Pending {
			t.Fatalf("%+v", ti)
		}
	}
	for _, want := range []string{"ask", "edit", "read", "shell", "todo", "write"} {
		if !contains(names, want) {
			t.Fatalf("missing %s in %v", want, names)
		}
	}
	if !reflect.DeepEqual(ov.Pending, []string{}) {
		t.Fatalf("pending must be an empty list: %#v", ov.Pending)
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
