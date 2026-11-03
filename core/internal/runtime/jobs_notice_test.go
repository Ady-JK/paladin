package runtime

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Ady-JK/paladin/core/internal/tools"
)

// A foreground command that outlives its wait becomes an auto job, and
// the session hears about it every notice interval, waking it when idle.
func TestAutoJobSendsRunningNoticeAndWakesIdleSession(t *testing.T) {
	oldInterval := jobNoticeEvery
	oldYield := tools.DefaultShellYieldMs
	jobNoticeEvery = 40 * time.Millisecond
	tools.DefaultShellYieldMs = 20
	defer func() {
		jobNoticeEvery = oldInterval
		tools.DefaultShellYieldMs = oldYield
	}()

	var mu sync.Mutex
	sawNotice := false
	rt, ws, sid, _ := agentRuntime(t, func(c modelCall) modelReply {
		mu.Lock()
		for _, u := range c.Users {
			if strings.Contains(u, "job_running") && strings.Contains(u, "running_for") {
				sawNotice = true
			}
		}
		mu.Unlock()
		if c.Results["shell"] != "" {
			return modelReply{Text: "done"}
		}
		return modelReply{Calls: []mockToolCall{call("shell", map[string]any{"command": "sleep 20"})}}
	})

	runParent(t, rt, sid, ws, "start it")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		ok := sawNotice
		mu.Unlock()
		if ok {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	ok := sawNotice
	mu.Unlock()
	if !ok {
		t.Fatal("no running notice reached the agent")
	}
	for _, id := range rt.jobs.running(sid) {
		if _, err := rt.shell.Call([]byte(`{"action":"cancel","job_id":"`+id+`"}`), &tools.ToolCtx{SessionID: sid}); err != nil {
			t.Fatal(err)
		}
	}
}
