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

func TestMCPMethods(t *testing.T) {
	rt := testRuntime(t)
	t.Cleanup(func() { rt.ShutdownTurns(3 * time.Second) })
	ws := t.TempDir()

	resp := callUnary(rt, "1", "mcpConfig", map[string]any{"scope": "global"})
	mustOK(t, resp)
	if cfg := resp.Result; !strings.Contains(fmt.Sprint(cfg), "mcpServers") {
		t.Fatalf("empty config template: %v", cfg)
	}
	mustFail(t, callUnary(rt, "2", "mcpSetConfig", map[string]any{"scope": "global", "text": `{"mcpServers":{"x":{"type":"sse","url":"http://x"}}}`}), "SSE")
	mustFail(t, callUnary(rt, "3", "mcpConfig", map[string]any{"scope": "folder"}), "folder")

	text := fmt.Sprintf(`{"mcpServers":{"dummy":{"command":%q}}}`, mcptest.Dummy(t))
	mustOK(t, callUnary(rt, "4", "mcpSetConfig", map[string]any{"scope": "folder", "cwd": ws, "text": text}))
	mustOK(t, callUnary(rt, "5", "mcpRestart", map[string]any{"scope": "folder", "cwd": ws, "name": "dummy"}))
	var ov runtime.MCPOverview
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		data, _ := json.Marshal(callUnary(rt, "6", "mcpList", map[string]any{"cwd": ws}).Result)
		_ = json.Unmarshal(data, &ov)
		if len(ov.Servers) == 1 && ov.Servers[0].State == "ready" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(ov.Servers) != 1 || ov.Servers[0].State != "ready" || len(ov.Servers[0].Tools) != 8 || !ov.Trusted || !ov.HasFolderConfig {
		t.Fatalf("%+v", ov)
	}
	mustOK(t, callUnary(rt, "7", "mcpSetEnabled", map[string]any{"scope": "folder", "cwd": ws, "name": "dummy", "enabled": false}))
	mustOK(t, callUnary(rt, "8", "mcpTrust", map[string]any{"cwd": ws, "trust": false}))
	data, _ := json.Marshal(callUnary(rt, "9", "mcpList", map[string]any{"cwd": ws}).Result)
	if !strings.Contains(string(data), `"trusted":false`) {
		t.Fatalf("%s", data)
	}
}

func TestMCPStatusEventsReachStdout(t *testing.T) {
	rt := testRuntime(t)
	t.Cleanup(func() { rt.ShutdownTurns(3 * time.Second) })
	installMCPSink(rt)
	text := fmt.Sprintf(`{"mcpServers":{"dummy":{"command":%q}}}`, mcptest.Dummy(t))
	out := captureStdout(t, func() {
		mustOK(t, callUnary(rt, "1", "mcpSetConfig", map[string]any{"scope": "global", "text": text}))
		mustOK(t, callUnary(rt, "2", "mcpRestart", map[string]any{"scope": "global", "name": "dummy"}))
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if s := rt.MCPList("").Servers; len(s) == 1 && s[0].State == "ready" {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
	})
	if !strings.Contains(out, `"event":"mcp/status"`) || !strings.Contains(out, `"state":"ready"`) {
		t.Fatalf("no mcp/status ready event in %s", out)
	}
}
