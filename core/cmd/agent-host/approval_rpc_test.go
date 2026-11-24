package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestStdioApprovalFlow(t *testing.T) {
	// A command the guard flags (a tunnel binary that does not exist, so it
	// fails harmlessly once approved).
	const flagged = "ngrok http 3000"
	toolArgs, err := json.Marshal(map[string]string{"reason": "test", "cmd": flagged})
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		mu.Lock()
		i := n
		n++
		mu.Unlock()
		if i == 0 {
			fmt.Fprintf(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","function":{"name":"shell","arguments":%q}}]}}]}`+"\n\ndata: [DONE]\n\n", string(toolArgs))
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()
	h := startLiveHost(t, buildHost(t), t.TempDir())
	h.call("addProvider", map[string]string{"name": "mock", "apiUrl": srv.URL, "apiKey": "sk", "apiType": "openai"})
	h.call("setDefaultProvider", map[string]string{"name": "mock"})
	cwd := t.TempDir()
	sid := h.call("openSession", map[string]string{"cwd": cwd})["result"].(map[string]any)["sessionId"].(string)

	id := h.send("submitChat", map[string]any{"sessionId": sid, "message": "go", "cwd": cwd, "permissions": "bogus"})
	if bad := h.waitLine("bad preset", func(m map[string]any) bool { return m["id"] == id }); bad["ok"] == true {
		t.Fatal("bad preset accepted")
	}

	h.call("submitChat", map[string]any{"sessionId": sid, "message": "go", "cwd": cwd})
	ev := h.event("chat/approval", sid, nil)
	ap := ev["approval"].(map[string]any)
	if ap["id"] != "c1" || ap["state"] != "pending" || ap["command"] != flagged {
		t.Fatalf("approval %v", ap)
	}
	att := h.call("attach", map[string]any{"sessionId": sid})["result"].(map[string]any)
	if turn := att["turn"].(map[string]any); turn["awaitingApproval"] != true {
		t.Fatalf("turn %v", turn)
	}
	h.call("answerApproval", map[string]any{"sessionId": sid, "callId": "c1", "decision": "once"})
	h.event("chat/approval", sid, func(m map[string]any) bool {
		a, _ := m["approval"].(map[string]any)
		return a["state"] == "approved"
	})
	h.event("turn/done", sid, nil)

	view := h.call("permissionsGet", map[string]any{"cwd": cwd})["result"].(map[string]any)
	if presets := view["presets"].([]any); len(presets) != 1 || presets[0] != "full-access" {
		t.Fatalf("presets: %v", view)
	}
}
