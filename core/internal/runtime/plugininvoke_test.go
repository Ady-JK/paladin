package runtime

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/permission"
	"github.com/Ady-JK/paladin/core/internal/tools"
)

type invokeProbe struct {
	ran chan struct{}
}

func (p *invokeProbe) Name() string { return "extension__plug__probe" }
func (p *invokeProbe) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{Name: p.Name(), Description: "probe", Parameters: map[string]any{"type": "object"}}
}
func (p *invokeProbe) Call(json.RawMessage, *tools.ToolCtx) (string, error) {
	select {
	case <-p.ran:
	default:
		close(p.ran)
	}
	return `{"ok":true}`, nil
}
func (p *invokeProbe) Extension() bool { return true }
func (p *invokeProbe) ExtensionInfo() tools.ExtensionInfo {
	return tools.ExtensionInfo{Plugin: "plug", Tool: "probe"}
}

func pluginInvokeFixture(t *testing.T) (*AgentRuntime, invokeProbe, string, string, chan PluginEvent) {
	t.Helper()
	rt, _ := testRuntime(t)
	if err := rt.store.Keys.Set("openrouter", "sk-test"); err != nil {
		t.Fatal(err)
	}
	pointMockAt(rt, "http://127.0.0.1:1")
	ws := t.TempDir()
	sess, err := rt.OpenSession("", ws)
	if err != nil {
		t.Fatal(err)
	}
	probe := invokeProbe{ran: make(chan struct{})}
	rt.tools.Register(&probe)
	approvals := make(chan PluginEvent, 1)
	rt.SetPluginSink(func(e PluginEvent) { approvals <- e })
	return rt, probe, sess.SessionID, ws, approvals
}

func runInvoke(rt *AgentRuntime, sessionID, ws string) <-chan PluginToolResult {
	out := make(chan PluginToolResult, 1)
	go func() {
		res, _ := rt.PluginInvoke(sessionID, ws, "plug", "panel", "extension__plug__probe", json.RawMessage(`{"x":1}`), false)
		out <- res
	}()
	return out
}

func TestPluginInvokeAsksAndRuns(t *testing.T) {
	rt, probe, sessionID, ws, approvals := pluginInvokeFixture(t)
	done := runInvoke(rt, sessionID, ws)
	ev := <-approvals
	if ev.Event != "plugin/approval" || ev.Approval == nil || ev.Approval.ID == "" {
		t.Fatalf("event = %+v", ev)
	}
	if ev.SessionID != sessionID {
		t.Fatalf("session = %q", ev.SessionID)
	}
	if _, err := rt.AnswerApproval(sessionID, ev.Approval.ID, permission.ReplyOnce); err != nil {
		t.Fatal(err)
	}
	res := <-done
	if res.IsError || !strings.Contains(res.Content, `"ok":true`) {
		t.Fatalf("result = %+v", res)
	}
	select {
	case <-probe.ran:
	default:
		t.Fatal("tool did not run")
	}
}

func TestPluginInvokeDeniedDoesNotRun(t *testing.T) {
	rt, probe, sessionID, ws, approvals := pluginInvokeFixture(t)
	done := runInvoke(rt, sessionID, ws)
	ev := <-approvals
	if ev.Approval == nil {
		t.Fatal("no approval")
	}
	if _, err := rt.AnswerApproval(sessionID, ev.Approval.ID, permission.ReplyDeny); err != nil {
		t.Fatal(err)
	}
	res := <-done
	if !res.IsError || !strings.Contains(res.Content, "denied") {
		t.Fatalf("result = %+v", res)
	}
	select {
	case <-probe.ran:
		t.Fatal("denied tool ran")
	default:
	}
}

func TestPluginInvokeRejectsAnotherPluginsTool(t *testing.T) {
	rt, _, sessionID, ws, _ := pluginInvokeFixture(t)
	_, err := rt.PluginInvoke(sessionID, ws, "other", "panel", "extension__plug__probe", json.RawMessage(`{}`), false)
	if err == nil || !strings.Contains(err.Error(), "another plugin") {
		t.Fatalf("err = %v", err)
	}
}

func TestPluginInvokeRejectsUnknownTool(t *testing.T) {
	rt, _, sessionID, ws, _ := pluginInvokeFixture(t)
	_, err := rt.PluginInvoke(sessionID, ws, "plug", "panel", "nope", nil, false)
	if err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("err = %v", err)
	}
}
