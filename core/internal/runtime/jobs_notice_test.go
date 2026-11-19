package runtime

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Ady-JK/paladin/core/internal/agent"
	"github.com/Ady-JK/paladin/core/internal/tools"
)

// setJobWatch shortens (or pins) the monitor's timings for one test and
// restores them afterwards. Register it before any cleanup that cancels
// jobs: cleanups run last-in first-out, so the jobs stop before the
// timings change back under their monitors.
func setJobWatch(t *testing.T, stall, stuck, progress time.Duration) {
	t.Helper()
	oldCheck, oldProgress, oldStall, oldStuck, oldYield := jobCheckEvery, jobProgressEvery, jobStallAfter, jobStuckAfter, tools.DefaultShellYieldMs
	jobCheckEvery, jobProgressEvery, jobStallAfter, jobStuckAfter = 20*time.Millisecond, progress, stall, stuck
	tools.DefaultShellYieldMs = 20
	t.Cleanup(func() {
		jobCheckEvery, jobProgressEvery, jobStallAfter, jobStuckAfter = oldCheck, oldProgress, oldStall, oldStuck
		tools.DefaultShellYieldMs = oldYield
	})
}

var watchEpoch = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

func atMinute(m float64) time.Time { return watchEpoch.Add(time.Duration(m * float64(time.Minute))) }

// A job that never prints: one stall notice at 10 minutes, one presumed-stuck
// notice at 30, then nothing however long it stays quiet.
func TestLogWatchSilentJobNoticesTwiceThenStops(t *testing.T) {
	setJobWatch(t, 10*time.Minute, 30*time.Minute, time.Hour)
	w := newLogWatch(atMinute(0), 0)

	if kind, _ := w.step(atMinute(9.9), 0); kind != "" {
		t.Fatalf("a notice before the silent window ended: %q", kind)
	}
	kind, silent := w.step(atMinute(10), 0)
	if kind != noticeStalled || silent != 10*time.Minute {
		t.Fatalf("at 10m: %q after %v, want %s after 10m", kind, silent, noticeStalled)
	}
	for _, m := range []float64{10.5, 11, 20, 29.9} {
		if kind, _ := w.step(atMinute(m), 0); kind != "" {
			t.Fatalf("repeat notice at %vm: %q", m, kind)
		}
	}
	if kind, _ := w.step(atMinute(30), 0); kind != noticeStuck {
		t.Fatalf("at 30m: %q, want %s", kind, noticeStuck)
	}
	for m := 30.5; m <= 300; m += 0.5 {
		if kind, _ := w.step(atMinute(m), 0); kind != "" {
			t.Fatalf("a notice at %vm after the presumed-stuck one: %q", m, kind)
		}
	}
}

// New output sends a job back to the start: it gets a full silent window
// again, and the stall and stuck notices can fire a second time.
func TestLogWatchOutputResetsTheSilentWindow(t *testing.T) {
	setJobWatch(t, 10*time.Minute, 30*time.Minute, time.Hour)
	w := newLogWatch(atMinute(0), 0)
	if kind, _ := w.step(atMinute(10), 0); kind != noticeStalled {
		t.Fatalf("first stall: %q", kind)
	}
	if kind, _ := w.step(atMinute(12), 40); kind != "" {
		t.Fatalf("output resumed but a notice came: %q", kind)
	}
	if kind, _ := w.step(atMinute(21.9), 40); kind != "" {
		t.Fatalf("under 10m of renewed silence: %q", kind)
	}
	kind, silent := w.step(atMinute(22), 40)
	if kind != noticeStalled || silent != 10*time.Minute {
		t.Fatalf("second stall: %q after %v", kind, silent)
	}
	if kind, _ := w.step(atMinute(42), 40); kind != noticeStuck {
		t.Fatalf("second stuck notice: %q", kind)
	}
}

// A job that keeps printing is mentioned once an hour and never reported
// as stalled.
func TestLogWatchProgressingJobGetsAnHourlyNotice(t *testing.T) {
	setJobWatch(t, 10*time.Minute, 30*time.Minute, time.Hour)
	w := newLogWatch(atMinute(0), 0)
	var got []string
	var at []float64
	size := int64(0)
	for m := 1.0; m <= 125; m++ {
		size += 100
		if kind, _ := w.step(atMinute(m), size); kind != "" {
			got, at = append(got, kind), append(at, m)
		}
	}
	if len(got) != 2 || got[0] != noticeProgress || got[1] != noticeProgress || at[0] != 60 || at[1] != 120 {
		t.Fatalf("notices %v at minutes %v, want two progress notices at 60 and 120", got, at)
	}
}

// A silence notice counts as the last word on the job, so the hourly note
// is measured from it rather than firing the moment output resumes.
func TestLogWatchHourlyNoticeRestartsAfterASilenceNotice(t *testing.T) {
	setJobWatch(t, 10*time.Minute, 30*time.Minute, time.Hour)
	w := newLogWatch(atMinute(0), 0)
	if kind, _ := w.step(atMinute(10), 0); kind != noticeStalled {
		t.Fatalf("stall: %q", kind)
	}
	if kind, _ := w.step(atMinute(30), 0); kind != noticeStuck {
		t.Fatalf("stuck: %q", kind)
	}
	size := int64(0)
	for m := 31.0; m < 90; m++ {
		size += 100
		if kind, _ := w.step(atMinute(m), size); kind != "" {
			t.Fatalf("a notice at %vm, before an hour passed since the last one: %q", m, kind)
		}
	}
	size += 100
	if kind, _ := w.step(atMinute(90), size); kind != noticeProgress {
		t.Fatalf("at 90m: %q, want %s", kind, noticeProgress)
	}
}

// A notice that lands after a turn's last round would wait for an unrelated
// event, and with no wait action nothing else wakes the agent. When the turn
// ends the queued notice is delivered, unless the user stopped that turn.
func TestAfterTurnWakesASessionWithAStrandedNotice(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	rt, ws, sid, _ := agentRuntime(t, func(c modelCall) modelReply {
		mu.Lock()
		seen = append(seen, c.Users...)
		mu.Unlock()
		return modelReply{Text: "ok"}
	})
	runParent(t, rt, sid, ws, "hello")
	// The turn's own done-callbacks run asynchronously; let them finish so
	// they cannot deliver the notice queued below.
	if !rt.hub.SettleDone(5 * time.Second) {
		t.Fatal("the first turn's done callbacks did not settle")
	}

	rt.jobs.mu.Lock()
	rt.jobs.notices[sid] = append(rt.jobs.notices[sid], agent.Notice{
		CallID: "job-9#job_stalled-1", Name: "shell",
		Arguments: `{"action":"notify","job_id":"job-9"}`,
		Content:   `{"event":"job_stalled","job_id":"job-9","last_output":"zzz-stranded"}`,
	})
	rt.jobs.mu.Unlock()
	sawStranded := func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, u := range seen {
			if strings.Contains(u, "zzz-stranded") {
				return true
			}
		}
		return false
	}

	// A turn the user stopped, or one that failed, is not resumed behind
	// the user's back.
	for _, info := range []TurnInfo{
		{SessionID: sid, Cwd: ws, State: TurnDone, Stopped: true},
		{SessionID: sid, Cwd: ws, State: TurnDone, Cancelled: true},
		{SessionID: sid, Cwd: ws, State: TurnDone, Error: "boom"},
		{SessionID: sid, Cwd: ws, State: TurnRunning},
	} {
		rt.jobs.afterTurn(info)
	}
	time.Sleep(300 * time.Millisecond)
	if sawStranded() {
		t.Fatal("a stopped, failed or unfinished turn was resumed")
	}

	rt.jobs.afterTurn(TurnInfo{SessionID: sid, Cwd: ws, State: TurnDone})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !sawStranded() {
		time.Sleep(20 * time.Millisecond)
	}
	if !sawStranded() {
		t.Fatal("the queued notice was never delivered after the turn ended")
	}
}

// watchJob runs one shell call through a real session and waits until the
// model has been shown one notice containing every wanted string. It
// returns that notice and cancels the job afterwards.
func watchJob(t *testing.T, args map[string]any, want ...string) string {
	t.Helper()
	var mu sync.Mutex
	var seen []string
	rt, ws, sid, _ := agentRuntime(t, func(c modelCall) modelReply {
		mu.Lock()
		seen = append(seen, c.Users...)
		mu.Unlock()
		if c.Results["shell"] != "" {
			return modelReply{Text: "done"}
		}
		return modelReply{Calls: []mockToolCall{call("shell", args)}}
	})
	t.Cleanup(func() {
		for _, id := range rt.jobs.running(sid) {
			_, _ = rt.shell.Call([]byte(`{"action":"cancel","job_id":"`+id+`"}`), &tools.ToolCtx{SessionID: sid})
		}
	})

	runParent(t, rt, sid, ws, "start it")
	find := func() string {
		mu.Lock()
		defer mu.Unlock()
		for _, u := range seen {
			ok := true
			for _, w := range want {
				if !strings.Contains(u, w) {
					ok = false
					break
				}
			}
			if ok {
				return u
			}
		}
		return ""
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if got := find(); got != "" {
			return got
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	t.Fatalf("no notice with %v reached the agent; it saw %d messages: %q", want, len(seen), seen)
	return ""
}

// A foreground command that outlives its wait becomes an auto job. When it
// goes quiet the session is woken with the tail of its log.
func TestAutoJobStallNoticeWakesIdleSessionWithLogTail(t *testing.T) {
	setJobWatch(t, 80*time.Millisecond, time.Hour, time.Hour)
	got := watchJob(t, map[string]any{"command": "echo hi-from-job; sleep 20"},
		noticeStalled, "last_output", "hi-from-job", "silent_for")
	if strings.Contains(got, "job_running") {
		t.Fatalf("the old five-minute running notice is back: %s", got)
	}
}

// A job started with background true is watched from the start, not only
// when a foreground call hands it over.
func TestExplicitBackgroundJobIsWatched(t *testing.T) {
	setJobWatch(t, 80*time.Millisecond, time.Hour, time.Hour)
	watchJob(t, map[string]any{"command": "sleep 20", "background": true},
		noticeStalled, "(no output yet)")
}

// The same job, still printing, gets the hourly note instead of a stall.
func TestProgressNoticeWhileTheLogKeepsGrowing(t *testing.T) {
	setJobWatch(t, time.Hour, 2*time.Hour, 150*time.Millisecond)
	got := watchJob(t, map[string]any{"command": "while true; do echo tick; sleep 0.01; done", "background": true},
		noticeProgress, "last_output", "tick")
	if strings.Contains(got, noticeStalled) {
		t.Fatalf("a job that is printing was reported stalled: %s", got)
	}
}

// notify:false is a request to hear nothing about the job, so no monitor
// runs for it and the agent is never woken for it.
func TestBackgroundJobWithNotifyFalseIsNotWatched(t *testing.T) {
	setJobWatch(t, 40*time.Millisecond, time.Hour, time.Hour)
	var mu sync.Mutex
	var seen []string
	rt, ws, sid, _ := agentRuntime(t, func(c modelCall) modelReply {
		mu.Lock()
		seen = append(seen, c.Users...)
		mu.Unlock()
		if c.Results["shell"] != "" {
			return modelReply{Text: "done"}
		}
		return modelReply{Calls: []mockToolCall{call("shell", map[string]any{"command": "sleep 20", "background": true, "notify": false})}}
	})
	t.Cleanup(func() {
		for _, id := range rt.jobs.running(sid) {
			_, _ = rt.shell.Call([]byte(`{"action":"cancel","job_id":"`+id+`"}`), &tools.ToolCtx{SessionID: sid})
		}
	})
	runParent(t, rt, sid, ws, "start it")
	time.Sleep(400 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	for _, u := range seen {
		if strings.Contains(u, "job_") && strings.Contains(u, "last_output") {
			t.Fatalf("a notify:false job sent a notice: %s", u)
		}
	}
	rt.jobs.mu.Lock()
	defer rt.jobs.mu.Unlock()
	if len(rt.jobs.stops) != 0 {
		t.Fatalf("a monitor is running for a notify:false job: %v", rt.jobs.stops)
	}
}
