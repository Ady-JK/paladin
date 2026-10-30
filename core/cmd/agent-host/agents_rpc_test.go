package main

import (
	"strings"
	"testing"
	"time"

	"github.com/Ady-JK/paladin/core/internal/runtime"
)

func TestAgentsMethods(t *testing.T) {
	rt := testRuntime(t)
	t.Cleanup(func() { rt.ShutdownTurns(3 * time.Second) })
	mustOK(t, callUnary(rt, "0", "addProvider", map[string]string{"name": "mock", "apiUrl": "http://127.0.0.1:1", "apiKey": "sk-live", "apiType": "openai"}))
	sess, err := rt.OpenSession("", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	list := decodeAs[[]runtime.AgentInfo](t, callUnary(rt, "1", "listAgents", map[string]any{"sessionId": sess.SessionID}))
	if len(list) != 0 {
		t.Fatalf("%+v", list)
	}
	mustFail(t, callUnary(rt, "2", "listAgents", map[string]any{}), "sessionId")
	mustFail(t, callUnary(rt, "3", "interruptAgent", map[string]any{"sessionId": sess.SessionID, "agentId": "nope"}), "unknown agent")
}

func TestAgentsChangedEventReachesStdout(t *testing.T) {
	rt := testRuntime(t)
	installAgentsSink(rt)
	out := captureStdout(t, func() {
		emitAgentsEvent(runtime.AgentsEvent{Event: "agents/changed", ParentSessionID: "p1", Agent: runtime.AgentInfo{ID: "c1", Status: "running"}})
	})
	if !strings.Contains(out, `"event":"agents/changed"`) || !strings.Contains(out, `"parentSessionId":"p1"`) || !strings.Contains(out, `"agentId":"c1"`) {
		t.Fatalf("%s", out)
	}
}
