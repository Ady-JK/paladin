package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// The app's running-jobs panel needs to see what the agent started and be
// able to stop it; the stop is recorded as the user's, not a timeout.
func TestStdioRunningJobsAndKill(t *testing.T) {
	var mu sync.Mutex
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		mu.Lock()
		i := n
		n++
		mu.Unlock()
		if i == 0 {
			fmt.Fprint(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","function":{"name":"shell","arguments":"{\"cmd\":\"sleep 30\",\"background\":true}"}}]}}]}`+"\n\ndata: [DONE]\n\n")
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
	h.call("submitChat", map[string]any{"sessionId": sid, "message": "run it", "cwd": cwd, "permissions": "full-access"})
	h.event("turn/done", sid, nil)

	jobs := h.call("runningJobs", map[string]any{})["result"].([]any)
	if len(jobs) != 1 {
		t.Fatalf("runningJobs = %v", jobs)
	}
	job := jobs[0].(map[string]any)
	if job["sessionId"] != sid || job["command"] != "sleep 30" {
		t.Fatalf("job = %v", job)
	}
	res := h.call("killJob", map[string]any{"id": job["id"]})["result"].(map[string]any)
	if res["status"] != "cancelled" {
		t.Fatalf("killJob = %v", res)
	}
	if left := h.call("runningJobs", map[string]any{})["result"].([]any); len(left) != 0 {
		t.Fatalf("job still listed: %v", left)
	}
	if bad := h.send("killJob", map[string]any{"id": "nope"}); h.waitLine("unknown job", func(m map[string]any) bool { return m["id"] == bad })["ok"] != false {
		t.Fatal("unknown job accepted")
	}
}
