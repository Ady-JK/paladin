package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Ady-JK/paladin/core/internal/mcp/mcptest"
	"github.com/Ady-JK/paladin/core/internal/runtime"
)

func decodeAs[T any](t *testing.T, resp Response) T {
	t.Helper()
	mustOK(t, resp)
	var v T
	data, _ := json.Marshal(resp.Result)
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestToolsMethods(t *testing.T) {
	rt := testRuntime(t)
	t.Cleanup(func() { rt.ShutdownTurns(3 * time.Second) })
	ws := t.TempDir()
	mustOK(t, callUnary(rt, "0", "addProvider", map[string]string{"name": "mock", "apiUrl": "http://127.0.0.1:1", "apiKey": "sk-live", "apiType": "openai"}))
	sess, err := rt.OpenSession("", ws)
	if err != nil {
		t.Fatal(err)
	}
	ov := decodeAs[runtime.ToolsOverview](t, callUnary(rt, "1", "toolsList", map[string]any{"cwd": ws, "sessionId": sess.SessionID}))
	if len(ov.Tools) == 0 || ov.Pending == nil {
		t.Fatalf("%+v", ov)
	}
	_ = decodeAs[runtime.ToolsOverview](t, callUnary(rt, "2", "toolsSet", map[string]any{"scope": "global", "cwd": ws, "names": []string{"shell"}, "enabled": false}))
	ov = decodeAs[runtime.ToolsOverview](t, callUnary(rt, "3", "toolsSet", map[string]any{"scope": "session", "cwd": ws, "sessionId": sess.SessionID, "names": []string{"shell"}, "enabled": true}))
	for _, ti := range ov.Tools {
		if ti.Name == "shell" && (!ti.Enabled || ti.Global == nil || *ti.Global) {
			t.Fatalf("%+v", ti)
		}
	}
	// enabled: null clears the switch.
	ov = decodeAs[runtime.ToolsOverview](t, callUnary(rt, "4", "toolsSet", map[string]any{"scope": "session", "cwd": ws, "sessionId": sess.SessionID, "names": []string{"shell"}, "enabled": nil}))
	for _, ti := range ov.Tools {
		if ti.Name == "shell" && (ti.Enabled || ti.Session != nil) {
			t.Fatalf("cleared %+v", ti)
		}
	}
	mustFail(t, callUnary(rt, "5", "toolsSet", map[string]any{"scope": "session", "cwd": ws, "names": []string{"shell"}, "enabled": true}), "sessionId")
	mustOK(t, callUnary(rt, "6", "toolsAccept", map[string]any{"cwd": ws, "sessionId": sess.SessionID}))
	mustFail(t, callUnary(rt, "7", "toolsAccept", map[string]any{"cwd": ws}), "sessionId")
}

func TestMCPParseAndAddMethods(t *testing.T) {
	rt := testRuntime(t)
	t.Cleanup(func() { rt.ShutdownTurns(3 * time.Second) })
	parsed := decodeAs[struct {
		Servers []struct {
			Name   string         `json:"name"`
			Config map[string]any `json:"config"`
		} `json:"servers"`
	}](t, callUnary(rt, "1", "mcpParse", map[string]any{"text": "TOKEN=x npx -y @modelcontextprotocol/server-github"}))
	if len(parsed.Servers) != 1 || parsed.Servers[0].Name != "github" || parsed.Servers[0].Config["command"] != "npx" {
		t.Fatalf("%+v", parsed)
	}
	mustFail(t, callUnary(rt, "2", "mcpParse", map[string]any{"text": ""}), "paste")

	add := map[string]any{"scope": "global", "servers": []map[string]any{{"name": "dummy", "config": map[string]any{"command": mcptest.Dummy(t)}}}}
	ov := decodeAs[runtime.MCPOverview](t, callUnary(rt, "3", "mcpAdd", add))
	if len(ov.Servers) != 1 || ov.Servers[0].Name != "dummy" {
		t.Fatalf("%+v", ov)
	}
	mustFail(t, callUnary(rt, "4", "mcpAdd", add), "already")
	add["replace"] = true
	mustOK(t, callUnary(rt, "5", "mcpAdd", add))
	// Added servers start on their own.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if s := rt.MCPList("").Servers; len(s) == 1 && s[0].State == "ready" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("added server never became ready: %s", fmt.Sprint(rt.MCPList("").Servers))
}

func TestToolsPendingEventReachesStdout(t *testing.T) {
	rt := testRuntime(t)
	installToolsSink(rt)
	out := captureStdout(t, func() {
		emitToolsEvent(runtime.ToolsEvent{Event: "tools/pending", SessionID: "s1", Pending: []string{"mcp__a__b"}})
	})
	if !strings.Contains(out, `"event":"tools/pending"`) || !strings.Contains(out, `"sessionId":"s1"`) {
		t.Fatalf("%s", out)
	}
}
