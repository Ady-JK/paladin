package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestStdioApprovalFlow(t *testing.T) {
	var mu sync.Mutex
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		mu.Lock()
		i := n
		n++
		mu.Unlock()
		if i == 0 {
			fmt.Fprint(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","function":{"name":"shell","arguments":"{\"cmd\":\"make -v >/dev/null 2>&1; echo ran\"}"}}]}}]}`+"\n\ndata: [DONE]\n\n")
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
	if ap["id"] != "c1" || ap["state"] != "pending" || ap["command"] == "" {
		t.Fatalf("approval %v", ap)
	}
	att := h.call("attach", map[string]any{"sessionId": sid})["result"].(map[string]any)
	if turn := att["turn"].(map[string]any); turn["awaitingApproval"] != true {
		t.Fatalf("turn %v", turn)
	}
	h.call("answerApproval", map[string]any{"sessionId": sid, "callId": "c1", "decision": "folder"})
	h.event("chat/approval", sid, func(m map[string]any) bool {
		a, _ := m["approval"].(map[string]any)
		return a["state"] == "approved"
	})
	h.event("turn/done", sid, nil)

	view := h.call("permissionsGet", map[string]any{"cwd": cwd})["result"].(map[string]any)
	if folder := view["folder"].([]any); len(folder) == 0 {
		t.Fatalf("folder rule not saved: %v", view)
	}
	h.call("permissionsSet", map[string]any{"scope": "folder", "cwd": cwd, "rules": []any{}})
	view = h.call("permissionsGet", map[string]any{"cwd": cwd})["result"].(map[string]any)
	if folder := view["folder"].([]any); len(folder) != 0 {
		t.Fatalf("folder rules not cleared: %v", view)
	}
	if _, ok := view["sandbox"].(map[string]any)["kind"]; !ok {
		t.Fatalf("sandbox kind missing: %v", view)
	}
}
