package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Ady-JK/paladin/core/internal/agent"
	"github.com/Ady-JK/paladin/core/internal/config"
	"github.com/Ady-JK/paladin/core/internal/tools"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

// modelCall is one chat request as the routing mock sees it.
type modelCall struct {
	Ctx     context.Context
	Model   string
	Child   bool
	System  string
	Users   []string
	Results map[string]string // tool name -> latest result content
	Tools   []string
}

type mockToolCall struct{ Name, Args string }

// modelReply is the mock's answer: tool calls, text, or an HTTP error.
type modelReply struct {
	Text   string
	Calls  []mockToolCall
	Status int
}

// routedModel answers every chat request through fn and records each
// call. Tool-call ids encode the tool name so results map back.
func routedModel(t *testing.T, fn func(modelCall) modelReply) (*httptest.Server, func() []modelCall) {
	t.Helper()
	var (
		mu    sync.Mutex
		calls []modelCall
		seq   atomic.Int64
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model    string `json:"model"`
			Messages []struct {
				Role       string `json:"role"`
				Content    any    `json:"content"`
				ToolCallID string `json:"tool_call_id"`
			} `json:"messages"`
			Tools []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
		}
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &req)
		c := modelCall{Ctx: r.Context(), Model: req.Model, Results: map[string]string{}}
		for _, m := range req.Messages {
			text := contentText(m.Content)
			switch m.Role {
			case "system":
				c.System += text
			case "user":
				c.Users = append(c.Users, text)
			case "tool":
				name, _, _ := strings.Cut(m.ToolCallID, "--")
				c.Results[name] = text
			}
		}
		c.Child = strings.Contains(c.System, "# SUBAGENT")
		for _, tl := range req.Tools {
			c.Tools = append(c.Tools, tl.Function.Name)
		}
		mu.Lock()
		calls = append(calls, c)
		mu.Unlock()
		reply := fn(c)
		if reply.Status != 0 {
			http.Error(w, `{"error":{"message":"mock failure"}}`, reply.Status)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if len(reply.Calls) > 0 {
			var parts []string
			for i, tc := range reply.Calls {
				id := fmt.Sprintf("%s--%d", tc.Name, seq.Add(1))
				parts = append(parts, fmt.Sprintf(`{"index":%d,"id":%q,"function":{"name":%q,"arguments":%q}}`, i, id, tc.Name, tc.Args))
			}
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[%s]}}]}\n\ndata: [DONE]\n\n", strings.Join(parts, ","))
			return
		}
		body, _ := json.Marshal(reply.Text)
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%s}}]}\n\ndata: [DONE]\n\n", body)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []modelCall {
		mu.Lock()
		defer mu.Unlock()
		return append([]modelCall(nil), calls...)
	}
}

func contentText(c any) string {
	switch v := c.(type) {
	case string:
		return v
	case []any:
		var b strings.Builder
		for _, p := range v {
			if m, ok := p.(map[string]any); ok {
				if s, ok := m["text"].(string); ok {
					b.WriteString(s)
				}
			}
		}
		return b.String()
	}
	return fmt.Sprint(c)
}

func call(name string, args any) mockToolCall {
	data, _ := json.Marshal(args)
	return mockToolCall{Name: name, Args: string(data)}
}

// agentRuntime is a runtime on a routed mock with one parent session.
func agentRuntime(t *testing.T, fn func(modelCall) modelReply) (*AgentRuntime, string, string, func() []modelCall) {
	t.Helper()
	rt, _ := testRuntime(t)
	_ = rt.store.Keys.Set("openrouter", "sk-test")
	srv, calls := routedModel(t, fn)
	pointMockAt(rt, srv.URL)
	ws := t.TempDir()
	sess, err := rt.OpenSession("", ws)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.ShutdownTurns(5 * time.Second) })
	return rt, ws, sess.SessionID, calls
}

func setLimits(t *testing.T, rt *AgentRuntime, turns uint32, children int) {
	t.Helper()
	if err := rt.updateSettings(func(s *Settings) { s.MaxConcurrentTurns, s.MaxSubagents = turns, children }); err != nil {
		t.Fatal(err)
	}
}

func runParent(t *testing.T, rt *AgentRuntime, sid, ws, msg string) TurnInfo {
	t.Helper()
	_, done, err := rt.SubmitTurn(agent.Request{SessionID: sid, Cwd: ws, Message: msg, Permissions: "full-access"}, SubmitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case info := <-done:
		return info
	case <-time.After(20 * time.Second):
		t.Fatalf("turn %q did not finish", msg)
	}
	return TurnInfo{}
}

func childIDs(t *testing.T, rt *AgentRuntime, ws, parentID string) []string {
	t.Helper()
	parent, err := rt.transcripts.Load(ws, parentID)
	if err != nil {
		t.Fatal(err)
	}
	return parent.Meta.Children
}

func loadChild(t *testing.T, rt *AgentRuntime, ws, id string) transcript.Session {
	t.Helper()
	s, err := rt.transcripts.Load(ws, id)
	if err != nil || s.Meta.Subagent == nil {
		t.Fatalf("child %s: %v", id, err)
	}
	return s
}

// childSettled waits until the child's finished turn is recorded: the hub
// goes idle slightly before its ordered done callback runs.
func childSettled(t *testing.T, rt *AgentRuntime, ws, id string) *transcript.Subagent {
	t.Helper()
	var sa *transcript.Subagent
	eventually(t, "child "+id+" settled", func() bool {
		sa = loadChild(t, rt, ws, id).Meta.Subagent
		return !rt.hub.Busy(id) && finalStatus(sa.Status)
	})
	return sa
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// spawnThenWait is a parent that spawns one child, waits for it and
// then answers with the wait result.
func spawnThenWait(spawn map[string]any, childReply func(modelCall) modelReply) func(modelCall) modelReply {
	return func(c modelCall) modelReply {
		if c.Child {
			return childReply(c)
		}
		if res, ok := c.Results["wait_agent"]; ok {
			return modelReply{Text: "parent saw: " + res}
		}
		if _, ok := c.Results["spawn_agent"]; ok {
			return modelReply{Calls: []mockToolCall{call("wait_agent", map[string]any{"timeout_ms": 10000})}}
		}
		if !strings.Contains(c.Users[len(c.Users)-1], "delegate") {
			return modelReply{Text: "noted"}
		}
		return modelReply{Calls: []mockToolCall{call("spawn_agent", spawn)}}
	}
}

func TestSpawnFreshChildRunsWithOneSlotAndReportsOnce(t *testing.T) {
	rt, ws, parentID, calls := agentRuntime(t, spawnThenWait(
		map[string]any{"agent_type": "explore", "task": "find the config loader"},
		func(modelCall) modelReply { return modelReply{Text: "loader is in config.go:42"} }))
	setLimits(t, rt, 1, 0)
	runParent(t, rt, parentID, ws, "earlier question")

	info := runParent(t, rt, parentID, ws, "delegate it")
	if info.Error != "" || !strings.Contains(info.Reply, "loader is in config.go:42") || !strings.Contains(info.Reply, `"timed_out":false`) {
		t.Fatalf("parent turn = %+v", info)
	}
	// The result counts as reported once the parent's turn end is processed.
	rt.hub.SettleDone(5 * time.Second)
	ids := childIDs(t, rt, ws, parentID)
	if len(ids) != 1 {
		t.Fatalf("children = %v", ids)
	}
	child := loadChild(t, rt, ws, ids[0])
	sa := child.Meta.Subagent
	if sa.ParentID != parentID || sa.Type != "explore" || sa.Status != AgentCompleted || !sa.Reported ||
		sa.Result != "loader is in config.go:42" || sa.ForkTurns != ForkNone || sa.SpawnCallID == "" || sa.ParentTurnID == "" {
		t.Fatalf("subagent meta = %+v", sa)
	}
	if child.Meta.ForkedFrom != "" {
		t.Fatalf("a subagent is not a user fork: %+v", child.Meta.ForkedFrom)
	}
	opened, err := rt.OpenSession(ids[0], ws)
	if err != nil || opened.Subagent == nil || opened.Subagent.ParentID != parentID || opened.Subagent.Status != AgentCompleted {
		t.Fatalf("openSession of a child = %+v, %v", opened, err)
	}
	parent, _ := rt.transcripts.Load(ws, parentID)
	if filepath.Dir(child.Dir) != filepath.Dir(parent.Dir) {
		t.Fatalf("child dir %s is not beside parent %s", child.Dir, parent.Dir)
	}

	var childCall *modelCall
	for _, c := range calls() {
		if c.Child {
			childCall = &c
		}
	}
	if childCall == nil {
		t.Fatal("child never called the model")
	}
	if !strings.Contains(childCall.System, "Role: explore") {
		t.Fatalf("child system prompt lacks its role: %q", childCall.System)
	}
	if len(childCall.Users) != 1 || childCall.Users[0] != "find the config loader" {
		t.Fatalf("fresh child should see only its task, got %q", childCall.Users)
	}
	for _, name := range childCall.Tools {
		if name != "read" && name != "shell" {
			t.Fatalf("child was offered %q (tools %v)", name, childCall.Tools)
		}
	}

	// Delivered by wait_agent, so no wake turn or later notice repeats it.
	time.Sleep(200 * time.Millisecond)
	if n := len(rt.Turns()); n != 0 {
		t.Fatalf("unexpected turns after delivery: %+v", rt.Turns())
	}
	if got := rt.subagents.notices(parentID, ws)(); len(got) != 0 {
		t.Fatalf("result delivered twice: %+v", got)
	}
}

func TestSpawnForkAllCopiesFinishedHistory(t *testing.T) {
	rt, ws, parentID, calls := agentRuntime(t, spawnThenWait(
		map[string]any{"agent_type": "reviewer", "task": "review what we discussed", "fork_turns": "all"},
		func(modelCall) modelReply { return modelReply{Text: "looks fine"} }))
	runParent(t, rt, parentID, ws, "remember the word pineapple")
	runParent(t, rt, parentID, ws, "now delegate")

	var child *modelCall
	for _, c := range calls() {
		if c.Child {
			child = &c
		}
	}
	if child == nil {
		t.Fatal("no child call")
	}
	joined := strings.Join(child.Users, "|")
	if !strings.Contains(joined, "remember the word pineapple") || !strings.Contains(joined, "review what we discussed") {
		t.Fatalf("forked child users = %q", child.Users)
	}
	if strings.Contains(joined, "now delegate") {
		t.Fatalf("the running turn leaked into the snapshot: %q", child.Users)
	}
	if loadChild(t, rt, ws, childIDs(t, rt, ws, parentID)[0]).Meta.Subagent.ForkTurns != ForkAll {
		t.Fatal("fork mode not recorded")
	}
}

func TestSpawnRejectsBadArguments(t *testing.T) {
	rt, ws, parentID, _ := agentRuntime(t, func(modelCall) modelReply { return modelReply{Text: "ok"} })
	runParent(t, rt, parentID, ws, "hi")
	tool, _ := rt.tools.Get("spawn_agent")
	for _, args := range []string{
		`{"agent_type":"wizard","task":"x"}`,
		`{"agent_type":"explore","task":"  "}`,
		`{"agent_type":"explore","task":"x","fork_turns":"some"}`,
		`{"agent_type":"explore","task":"x","model":"no-such-model"}`,
		`{"agent_type":"explore","task":"x","bogus":1}`,
	} {
		out, err := tool.Call(json.RawMessage(args), toolCtx(parentID, ws))
		if err == nil {
			t.Errorf("%s accepted: %s", args, out)
		}
	}
	if len(childIDs(t, rt, ws, parentID)) != 0 {
		t.Fatal("a rejected spawn created a child")
	}
}

func TestOnlyTheParentControlsItsChildren(t *testing.T) {
	rt, ws, parentID, _ := agentRuntime(t, func(c modelCall) modelReply { return modelReply{Text: "done"} })
	runParent(t, rt, parentID, ws, "hi")
	info, err := rt.subagents.spawn(parentID, spawnArgs{Kind: subagentKinds[0], Task: "look", ForkTurns: ForkNone})
	if err != nil {
		t.Fatal(err)
	}
	rt.hub.WaitIdle(info.ID, 5*time.Second)

	other, err := rt.OpenSession("", ws)
	if err != nil {
		t.Fatal(err)
	}
	runParent(t, rt, other.SessionID, ws, "hi")
	if _, err := rt.subagents.followup(other.SessionID, info.ID, "more"); err == nil || !strings.Contains(err.Error(), "not a subagent of this session") {
		t.Fatalf("stranger followup err = %v", err)
	}
	if _, _, err := rt.subagents.interrupt(other.SessionID, info.ID); err == nil {
		t.Fatal("stranger interrupted a child")
	}
	if _, err := rt.subagents.wait(context.Background(), other.SessionID, []string{info.Name}, time.Second); err == nil {
		t.Fatal("stranger waited on a child by name")
	}
	if _, err := rt.subagents.spawn(info.ID, spawnArgs{Kind: subagentKinds[0], Task: "nested", ForkTurns: ForkNone}); err == nil ||
		!strings.Contains(err.Error(), "cannot spawn") {
		t.Fatalf("child spawned a grandchild: %v", err)
	}
	if _, err := rt.subagents.followup(parentID, "nobody", "x"); err == nil || !strings.Contains(err.Error(), "unknown agent") {
		t.Fatalf("unknown target err = %v", err)
	}
	if list, err := rt.subagents.list(other.SessionID); err != nil || len(list) != 0 {
		t.Fatalf("stranger list = %+v, %v", list, err)
	}
}

func TestChildToolsAreReadOnlyAndNarrowedByTheParent(t *testing.T) {
	marker := ""
	rt, ws, parentID, calls := agentRuntime(t, func(c modelCall) modelReply {
		if !c.Child {
			return modelReply{Text: "ok"}
		}
		if res, ok := c.Results["shell"]; ok {
			return modelReply{Text: "shell said: " + res}
		}
		if _, ok := c.Results["write"]; ok {
			return modelReply{Calls: []mockToolCall{call("shell", map[string]any{"cmd": "touch " + marker})}}
		}
		return modelReply{Calls: []mockToolCall{call("write", map[string]any{"path": marker, "content": "x"})}}
	})
	marker = filepath.Join(ws, "child-wrote-this")
	runParent(t, rt, parentID, ws, "hi")
	if _, err := rt.ToolsSet(ToolScopeSession, ws, parentID, []string{"read"}, new(bool)); err != nil {
		t.Fatal(err)
	}

	var ids []string
	for range 2 {
		info, err := rt.subagents.spawn(parentID, spawnArgs{Kind: subagentKinds[0], Task: "write a file", ForkTurns: ForkNone})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, info.ID)
	}
	for _, id := range ids {
		childSettled(t, rt, ws, id)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("a read-only child changed the shared workspace: %v", err)
	}
	for _, id := range ids {
		sa := loadChild(t, rt, ws, id).Meta.Subagent
		if sa.Status != AgentCompleted || !strings.Contains(sa.Result, "plan mode") {
			t.Fatalf("child %s = %+v", id, sa)
		}
	}
	for _, c := range calls() {
		if c.Child && !slices.Equal(c.Tools, []string{"shell"}) {
			t.Fatalf("child tools = %v, want only shell (parent switched read off)", c.Tools)
		}
	}
}

func TestChildFailureIsReportedAsFailed(t *testing.T) {
	rt, ws, parentID, _ := agentRuntime(t, spawnThenWait(
		map[string]any{"agent_type": "explore", "task": "boom"},
		func(modelCall) modelReply { return modelReply{Status: http.StatusBadRequest} }))
	info := runParent(t, rt, parentID, ws, "delegate")
	if !strings.Contains(info.Reply, `"status":"failed"`) {
		t.Fatalf("parent saw %q", info.Reply)
	}
	sa := loadChild(t, rt, ws, childIDs(t, rt, ws, parentID)[0]).Meta.Subagent
	if sa.Status != AgentFailed || sa.Error == "" {
		t.Fatalf("child = %+v", sa)
	}
}

// blockingChild blocks every child call until its request is
// cancelled or release is closed.
func blockingChild(release <-chan struct{}, started chan<- struct{}) func(modelCall) modelReply {
	return func(c modelCall) modelReply {
		if !c.Child {
			return modelReply{Text: "ok"}
		}
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-c.Ctx.Done():
		case <-release:
		}
		return modelReply{Text: "child finished"}
	}
}

func TestInterruptRunningChildIsQuietAndFollowupResumes(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	rt, ws, parentID, calls := agentRuntime(t, blockingChild(release, started))
	runParent(t, rt, parentID, ws, "hi")
	info, err := rt.subagents.spawn(parentID, spawnArgs{Kind: subagentKinds[0], Task: "first task", ForkTurns: ForkNone})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	tagged := false
	for _, turn := range rt.Turns() {
		tagged = tagged || (turn.SessionID == info.ID && turn.Parent == parentID)
	}
	if !tagged {
		t.Fatalf("child turn not tagged with its parent: %+v", rt.Turns())
	}
	if _, err := rt.subagents.followup(parentID, info.Name, "queued behind"); err != nil {
		t.Fatal(err)
	}
	got, stopped, err := rt.subagents.interrupt(parentID, info.ID)
	if err != nil || !stopped || got.Status != AgentInterrupted {
		t.Fatalf("interrupt = %+v %v %v", got, stopped, err)
	}
	if rt.hub.Busy(info.ID) {
		t.Fatal("queued follow-up survived the interrupt")
	}
	if _, stopped, _ := rt.subagents.interrupt(parentID, info.ID); stopped {
		t.Fatal("second interrupt reported a stop")
	}
	if _, err := rt.subagents.message(parentID, info.ID, "hello?"); err == nil || !strings.Contains(err.Error(), "followup_task") {
		t.Fatalf("message to a stopped child: %v", err)
	}
	rt.hub.SettleDone(5 * time.Second)
	if got := rt.subagents.notices(parentID, ws)(); len(got) != 0 {
		t.Fatalf("a parent-requested interrupt was reported back: %+v", got)
	}
	// The dropped follow-up and the halted turn each end in an onDone,
	// in either order; whichever comes second must stay quiet too.
	rt.subagents.onDone(TurnInfo{SessionID: info.ID, Cwd: ws, Stopped: true})
	rt.subagents.mu.Lock()
	pending := slices.Clone(rt.subagents.pending[parentID])
	rt.subagents.mu.Unlock()
	if !loadChild(t, rt, ws, info.ID).Meta.Subagent.Reported || len(pending) != 0 || rt.hub.Busy(parentID) {
		t.Fatalf("a late done for a quiet interrupt was reported back: pending %v, parent woken %v", pending, rt.hub.Busy(parentID))
	}

	close(release)
	if _, err := rt.subagents.followup(parentID, info.ID, "second task"); err != nil {
		t.Fatal(err)
	}
	var sa *transcript.Subagent
	eventually(t, "follow-up recorded", func() bool {
		sa = loadChild(t, rt, ws, info.ID).Meta.Subagent
		return sa.Status == AgentCompleted
	})
	if sa.Result != "child finished" {
		t.Fatalf("after follow-up = %+v", sa)
	}
	var last modelCall
	for _, c := range calls() {
		if c.Child {
			last = c
		}
	}
	if !slices.Contains(last.Users, "first task") || !slices.Contains(last.Users, "second task") {
		t.Fatalf("follow-up should keep the child's conversation: %q", last.Users)
	}
}

func TestMessageSteersARunningChild(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	var steered atomic.Bool
	rt, ws, parentID, _ := agentRuntime(t, func(c modelCall) modelReply {
		if c.Child && strings.Contains(strings.Join(c.Users, "|"), "focus on tests") {
			steered.Store(true)
			return modelReply{Text: "steered"}
		}
		return blockingChild(release, started)(c)
	})
	runParent(t, rt, parentID, ws, "hi")
	info, err := rt.subagents.spawn(parentID, spawnArgs{Kind: subagentKinds[0], Task: "look around", ForkTurns: ForkNone})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if _, err := rt.subagents.message(parentID, info.ID, "focus on tests"); err != nil {
		t.Fatal(err)
	}
	close(release)
	rt.hub.WaitIdle(info.ID, 10*time.Second)
	if !steered.Load() {
		t.Fatal("the child never saw the parent's message")
	}
}

func TestInterruptWhileQueuedNeverCallsTheModel(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	var childCalls atomic.Int32
	rt, ws, parentID, _ := agentRuntime(t, func(c modelCall) modelReply {
		if c.Child {
			childCalls.Add(1)
			return modelReply{Text: "ran"}
		}
		if strings.Contains(strings.Join(c.Users, "|"), "hog the slot") {
			<-release
		}
		return modelReply{Text: "ok"}
	})
	runParent(t, rt, parentID, ws, "hi")
	setLimits(t, rt, 1, 0)
	other, _ := rt.OpenSession("", ws)
	if _, _, err := rt.SubmitTurn(agent.Request{SessionID: other.SessionID, Cwd: ws, Message: "hog the slot", Permissions: "full-access"}, SubmitOpts{}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "hog running", func() bool { return rt.hub.Running(other.SessionID) })
	info, err := rt.subagents.spawn(parentID, spawnArgs{Kind: subagentKinds[0], Task: "wait in line", ForkTurns: ForkNone})
	if err != nil || info.Status != AgentQueued {
		t.Fatalf("spawn = %+v %v", info, err)
	}
	if _, stopped, err := rt.subagents.interrupt(parentID, info.ID); err != nil || !stopped {
		t.Fatalf("interrupt queued: %v %v", stopped, err)
	}
	eventually(t, "child recorded interrupted", func() bool {
		return loadChild(t, rt, ws, info.ID).Meta.Subagent.Status == AgentInterrupted
	})
	if childCalls.Load() != 0 {
		t.Fatal("a cancelled queued child still ran")
	}
}

func TestParentStopCascadesToChildren(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	started := make(chan struct{}, 1)
	rt, ws, parentID, _ := agentRuntime(t, func(c modelCall) modelReply {
		if c.Child {
			return blockingChild(release, started)(c)
		}
		if _, ok := c.Results["spawn_agent"]; ok {
			return modelReply{Calls: []mockToolCall{call("wait_agent", map[string]any{"timeout_ms": 600000})}}
		}
		return modelReply{Calls: []mockToolCall{call("spawn_agent", map[string]any{"agent_type": "explore", "task": "forever"})}}
	})
	setLimits(t, rt, 1, 0)
	_, done, err := rt.SubmitTurn(agent.Request{SessionID: parentID, Cwd: ws, Message: "go", Permissions: "full-access"}, SubmitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	rt.StopIn(parentID, ws)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("parent did not stop while waiting")
	}
	id := childIDs(t, rt, ws, parentID)[0]
	if sa := childSettled(t, rt, ws, id); sa.Status != AgentInterrupted || !sa.Reported {
		t.Fatalf("child after parent stop = %+v", sa)
	}
	time.Sleep(100 * time.Millisecond)
	if rt.hub.Busy(parentID) {
		t.Fatal("a quiet stop woke the parent")
	}
}

func TestIdleParentIsWokenWithTheResult(t *testing.T) {
	release := make(chan struct{})
	rt, ws, parentID, _ := agentRuntime(t, func(c modelCall) modelReply {
		if c.Child {
			<-release
			return modelReply{Text: "background answer"}
		}
		if last := c.Users[len(c.Users)-1]; strings.Contains(last, "subagent_finished") {
			return modelReply{Text: "woken with " + last}
		}
		if _, ok := c.Results["spawn_agent"]; ok {
			return modelReply{Text: "spawned; carrying on"}
		}
		return modelReply{Calls: []mockToolCall{call("spawn_agent", map[string]any{"agent_type": "explore", "task": "bg"})}}
	})
	if info := runParent(t, rt, parentID, ws, "start"); info.Reply != "spawned; carrying on" {
		t.Fatalf("first turn = %+v", info)
	}
	close(release)
	var reply string
	eventually(t, "wake turn", func() bool {
		hist, _ := rt.SessionHistory(parentID, ws)
		for _, m := range hist {
			if strings.Contains(m.Content, "woken with") {
				reply = m.Content
				return !rt.hub.Busy(parentID)
			}
		}
		return false
	})
	if !strings.Contains(reply, "background answer") || !strings.Contains(reply, "subagent_finished") {
		t.Fatalf("wake reply = %q", reply)
	}
	id := childIDs(t, rt, ws, parentID)[0]
	eventually(t, "woken delivery reported", func() bool { return loadChild(t, rt, ws, id).Meta.Subagent.Reported })
	if !loadChild(t, rt, ws, id).Meta.Subagent.Reported {
		t.Fatal("woken delivery not marked reported")
	}
}

func TestChildLimitCountsOnlyActiveChildren(t *testing.T) {
	release := make(chan struct{})
	rt, ws, parentID, _ := agentRuntime(t, blockingChild(release, make(chan struct{}, 8)))
	runParent(t, rt, parentID, ws, "hi")
	setLimits(t, rt, 0, 2)
	for i := range 2 {
		if _, err := rt.subagents.spawn(parentID, spawnArgs{Kind: subagentKinds[0], Task: fmt.Sprint(i), ForkTurns: ForkNone}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := rt.subagents.spawn(parentID, spawnArgs{Kind: subagentKinds[0], Task: "3", ForkTurns: ForkNone}); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("third spawn err = %v", err)
	}
	close(release)
	for _, id := range childIDs(t, rt, ws, parentID) {
		rt.hub.WaitIdle(id, 10*time.Second)
	}
	eventually(t, "children recorded", func() bool {
		list, _ := rt.subagents.list(parentID)
		return len(list) == 2 && finalStatus(list[0].Status) && finalStatus(list[1].Status)
	})
	if _, err := rt.subagents.spawn(parentID, spawnArgs{Kind: subagentKinds[0], Task: "after", ForkTurns: ForkNone}); err != nil {
		t.Fatalf("finished children still counted: %v", err)
	}
}

func TestRestartKeepsChildrenAndDeliversUnreportedResults(t *testing.T) {
	rt, ws, parentID, _ := agentRuntime(t, func(c modelCall) modelReply { return modelReply{Text: "child result"} })
	runParent(t, rt, parentID, ws, "hi")
	// Results stay undelivered until the drain below (no idle wake).
	noWake(rt, parentID)
	done, err := rt.subagents.spawn(parentID, spawnArgs{Kind: subagentKinds[0], Task: "finish", ForkTurns: ForkNone})
	if err != nil {
		t.Fatal(err)
	}
	childSettled(t, rt, ws, done.ID)
	// A child whose turn a crash cut off still says running on disk.
	stale, err := rt.subagents.spawn(parentID, spawnArgs{Kind: subagentKinds[1], Task: "crash", ForkTurns: ForkNone})
	if err != nil {
		t.Fatal(err)
	}
	childSettled(t, rt, ws, stale.ID)
	// Drain everything the first runtime would have delivered.
	rt.subagents.notices(parentID, ws)()
	rt.ShutdownTurns(5 * time.Second)
	_ = rt.transcripts.UpdateMeta(ws, done.ID, func(m *transcript.Meta) error { m.Subagent.Reported = false; return nil })
	_ = rt.transcripts.UpdateMeta(ws, stale.ID, func(m *transcript.Meta) error {
		m.Subagent.Status, m.Subagent.Reported = AgentRunning, false
		return nil
	})

	again := WithKeyStore(rt.dataDir, config.NewConfig(rt.dataDir), config.NewMemoryKeyStore())
	t.Cleanup(func() { again.ShutdownTurns(5 * time.Second) })
	list, err := again.subagents.list(parentID)
	if err != nil || len(list) != 2 {
		t.Fatalf("list after restart = %+v, %v", list, err)
	}
	if list[0].Status != AgentCompleted || list[1].Status != AgentInterrupted {
		t.Fatalf("statuses after restart = %+v", list)
	}
	sessions, _ := again.ListSessions(ws, 0)
	var nested int
	for _, s := range sessions {
		if s.Subagent != nil && s.Subagent.ParentID == parentID {
			nested++
		}
		if s.SessionID == parentID && len(s.Children) != 2 {
			t.Fatalf("parent children = %v", s.Children)
		}
	}
	if nested != 2 {
		t.Fatalf("listed %d children", nested)
	}
	notices := again.subagents.notices(parentID, ws)()
	if len(notices) != 2 || !strings.Contains(notices[0].Content, "child result") || !strings.Contains(notices[1].Content, "app stopped") {
		t.Fatalf("notices after restart = %+v", notices)
	}
	if again.subagents.notices(parentID, ws)() != nil {
		t.Fatal("restart notices repeated")
	}
}

func TestDeletingTheParentDeletesItsChildren(t *testing.T) {
	rt, ws, parentID, _ := agentRuntime(t, func(c modelCall) modelReply { return modelReply{Text: "ok"} })
	runParent(t, rt, parentID, ws, "hi")
	info, err := rt.subagents.spawn(parentID, spawnArgs{Kind: subagentKinds[0], Task: "x", ForkTurns: ForkNone})
	if err != nil {
		t.Fatal(err)
	}
	rt.hub.WaitIdle(info.ID, 10*time.Second)
	child := loadChild(t, rt, ws, info.ID)
	if _, err := rt.DeleteSession(parentID, ws); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(child.Dir); !os.IsNotExist(err) {
		t.Fatalf("child dir survived: %v", err)
	}
}

func toolCtx(sessionID, ws string) *tools.ToolCtx {
	return &tools.ToolCtx{Workspace: ws, Cancel: context.Background(), SessionID: sessionID, CallID: "test-call"}
}

func TestUserTurnsIntoAChildAreRefused(t *testing.T) {
	rt, ws, parentID, _ := agentRuntime(t, func(modelCall) modelReply { return modelReply{Text: "ok"} })
	runParent(t, rt, parentID, ws, "hi")
	info, err := rt.subagents.spawn(parentID, spawnArgs{Kind: subagentKinds[0], Task: "x", ForkTurns: ForkNone})
	if err != nil {
		t.Fatal(err)
	}
	childSettled(t, rt, ws, info.ID)
	if _, _, err := rt.SubmitTurn(agent.Request{SessionID: info.ID, Cwd: ws, Message: "hijack"}, SubmitOpts{}); err == nil ||
		!strings.Contains(err.Error(), "subagent") {
		t.Fatalf("user turn into child: %v", err)
	}
	if _, err := rt.SteerTurn(info.ID, "hijack", "hijack"); err == nil {
		t.Fatal("user steered a child")
	}
	list, err := rt.ListAgents(parentID)
	if err != nil || len(list) != 1 || list[0].ID != info.ID {
		t.Fatalf("ListAgents = %+v %v", list, err)
	}
	if _, err := rt.InterruptAgent(parentID, info.ID); err != nil {
		t.Fatalf("InterruptAgent on an idle child: %v", err)
	}
}

func TestChildRunsOnTheParentTurnsModel(t *testing.T) {
	rt, ws, parentID, calls := agentRuntime(t, spawnThenWait(
		map[string]any{"agent_type": "explore", "task": "look"},
		func(modelCall) modelReply { return modelReply{Text: "seen"} }))
	provider, model := "openrouter", "parent/turn-model"
	_, done, err := rt.SubmitTurn(agent.Request{SessionID: parentID, Cwd: ws, Message: "delegate",
		Provider: &provider, Model: &model, Permissions: "full-access"}, SubmitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	<-done
	child := loadChild(t, rt, ws, childIDs(t, rt, ws, parentID)[0])
	if child.Meta.Model == nil || *child.Meta.Model != model || child.Meta.Provider == nil || *child.Meta.Provider != provider {
		t.Fatalf("child meta provider/model = %v/%v", child.Meta.Provider, child.Meta.Model)
	}
	for _, c := range calls() {
		if c.Child && c.Model != model {
			t.Fatalf("child called model %q, want the parent turn's %q", c.Model, model)
		}
	}
}

// spawn_agent is parallel-safe, so simultaneous spawns must still
// respect the child limit and get distinct names.
func TestParallelSpawnsRespectTheLimitAndNames(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	rt, ws, parentID, _ := agentRuntime(t, blockingChild(release, make(chan struct{}, 16)))
	runParent(t, rt, parentID, ws, "hi")
	setLimits(t, rt, 0, 2)
	const tries = 8
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		names []string
		fails int
	)
	start := make(chan struct{})
	for range tries {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			info, err := rt.subagents.spawn(parentID, spawnArgs{Kind: subagentKinds[0], Task: "t", Name: "scout", ForkTurns: ForkNone})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if !strings.Contains(err.Error(), "limit") {
					t.Errorf("unexpected spawn error: %v", err)
				}
				fails++
				return
			}
			names = append(names, info.Name)
		}()
	}
	close(start)
	wg.Wait()
	if len(names) != 2 || fails != tries-2 {
		t.Fatalf("spawned %v (%d refused); want exactly 2 under the limit", names, fails)
	}
	if names[0] == names[1] {
		t.Fatalf("parallel spawns share the name %q", names[0])
	}
	if ids := childIDs(t, rt, ws, parentID); len(ids) != 2 {
		t.Fatalf("parent lists %d children", len(ids))
	}
}

// A result handed to a parent turn counts as reported only once that
// turn is saved: a turn that never persists (crash, failed write)
// must leave it to be delivered again.
func TestResultIsReportedOnlyWhenTheParentTurnPersists(t *testing.T) {
	// No parent turn has run, so no real wake turn races these
	// hand-driven ones for the result.
	rt, ws, parentID, _ := agentRuntime(t, func(c modelCall) modelReply { return modelReply{Text: "child result"} })
	info, err := rt.subagents.spawn(parentID, spawnArgs{Kind: subagentKinds[0], Task: "finish", ForkTurns: ForkNone})
	if err != nil {
		t.Fatal(err)
	}
	childSettled(t, rt, ws, info.ID)
	rt.hub.SettleDone(5 * time.Second)

	reported := func() bool { return loadChild(t, rt, ws, info.ID).Meta.Subagent.Reported }
	if got := rt.subagents.deliverNotices(parentID, ws, "turn-1"); len(got) != 1 {
		t.Fatalf("first delivery = %+v", got)
	}
	if reported() {
		t.Fatal("marked reported before the parent turn was saved: a crash now loses the result")
	}
	// A restart at this point redelivers from disk.
	again := WithKeyStore(rt.dataDir, config.NewConfig(rt.dataDir), config.NewMemoryKeyStore())
	t.Cleanup(func() { again.ShutdownTurns(5 * time.Second) })
	if got := again.subagents.deliverNotices(parentID, ws, "turn-x"); len(got) != 1 || !strings.Contains(got[0].Content, "child result") {
		t.Fatalf("after a crash the result is lost: %+v", got)
	}

	// The turn ends without persisting: the result goes back in the mailbox.
	rt.subagents.onDone(TurnInfo{SessionID: parentID, TurnID: "turn-1", Cwd: ws, Stopped: true})
	if reported() {
		t.Fatal("an unsaved turn marked the result reported")
	}
	if !rt.subagents.hasPending(parentID, ws) {
		t.Fatal("an unsaved delivery was dropped instead of requeued")
	}
	if got := rt.subagents.deliverNotices(parentID, ws, "turn-2"); len(got) != 1 {
		t.Fatalf("redelivery = %+v", got)
	}
	rt.subagents.onDone(TurnInfo{SessionID: parentID, TurnID: "turn-2", Cwd: ws, Persisted: true})
	if !reported() {
		t.Fatal("a saved delivery was not marked reported")
	}
	if rt.subagents.hasPending(parentID, ws) || len(rt.subagents.deliverNotices(parentID, ws, "turn-3")) != 0 {
		t.Fatal("a saved delivery was delivered again")
	}
}

// A follow-up that reactivates a finished child takes a slot too.
func TestFollowupRespectsTheChildLimit(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	rt, ws, parentID, _ := agentRuntime(t, func(c modelCall) modelReply {
		if c.Child && strings.Contains(c.Users[len(c.Users)-1], "hold") {
			<-release
		}
		return modelReply{Text: "ok"}
	})
	runParent(t, rt, parentID, ws, "hi")
	setLimits(t, rt, 0, 1)
	done, err := rt.subagents.spawn(parentID, spawnArgs{Kind: subagentKinds[0], Task: "quick", ForkTurns: ForkNone})
	if err != nil {
		t.Fatal(err)
	}
	childSettled(t, rt, ws, done.ID)
	if _, err := rt.subagents.spawn(parentID, spawnArgs{Kind: subagentKinds[0], Task: "hold", ForkTurns: ForkNone}); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.subagents.followup(parentID, done.ID, "more"); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("follow-up over the limit: err = %v", err)
	}
	if st := loadChild(t, rt, ws, done.ID).Meta.Subagent.Status; st != AgentCompleted {
		t.Fatalf("refused follow-up changed the child to %s", st)
	}
}
