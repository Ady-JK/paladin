package runtime

import (
	"strings"
	"testing"
	"time"

	"github.com/Ady-JK/paladin/core/internal/config"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

// noWake keeps finished children from waking the idle parent, so a
// test decides when results are delivered.
func noWake(rt *AgentRuntime, parentID string) {
	rt.subagents.mu.Lock()
	delete(rt.subagents.last, parentID)
	rt.subagents.mu.Unlock()
}

// A finished child's completion id names that child turn, so the same
// result always carries the same id however often it is offered.
func TestCompletionIDNamesTheChildTurn(t *testing.T) {
	rt, ws, parentID, _ := agentRuntime(t, func(c modelCall) modelReply { return modelReply{Text: "child result"} })
	runParent(t, rt, parentID, ws, "hi")
	noWake(rt, parentID)
	info, err := rt.subagents.spawn(parentID, spawnArgs{Kind: subagentKinds[0], Task: "finish", ForkTurns: ForkNone})
	if err != nil {
		t.Fatal(err)
	}
	sa := childSettled(t, rt, ws, info.ID)
	if !strings.HasPrefix(sa.CompletionID, info.ID+"#") || strings.Contains(sa.CompletionID, "#agent-") {
		t.Fatalf("completion id = %q", sa.CompletionID)
	}
	notices := rt.subagents.notices(parentID, ws)()
	if len(notices) != 1 || notices[0].CallID != sa.CompletionID {
		t.Fatalf("notice call id %+v, want %q", notices, sa.CompletionID)
	}
	view := agentView(AgentInfo{ID: info.ID, Result: "r", CompletionID: sa.CompletionID}, true)
	if view["completion_id"] != sa.CompletionID {
		t.Fatalf("wait view lacks completion id: %v", view)
	}
	if _, ok := agentView(AgentInfo{ID: info.ID, CompletionID: sa.CompletionID}, false)["completion_id"]; ok {
		t.Fatal("list view should not carry a completion id")
	}
}

// A crash after the parent saved a result but before the child was
// marked reported must not deliver that result a second time.
func TestRestartReconcilesResultsTheParentAlreadySaved(t *testing.T) {
	rt, ws, parentID, _ := agentRuntime(t, func(c modelCall) modelReply { return modelReply{Text: "child result"} })
	runParent(t, rt, parentID, ws, "hi")
	noWake(rt, parentID)
	saved, err := rt.subagents.spawn(parentID, spawnArgs{Kind: subagentKinds[0], Task: "a", ForkTurns: ForkNone})
	if err != nil {
		t.Fatal(err)
	}
	lost, err := rt.subagents.spawn(parentID, spawnArgs{Kind: subagentKinds[1], Task: "b", ForkTurns: ForkNone})
	if err != nil {
		t.Fatal(err)
	}
	savedSA := childSettled(t, rt, ws, saved.ID)
	childSettled(t, rt, ws, lost.ID)
	rt.subagents.notices(parentID, ws)()
	rt.ShutdownTurns(5 * time.Second)

	// The parent transcript holds the first result's notice row; the
	// crash came before either child was marked reported.
	parent, err := rt.transcripts.Load(ws, parentID)
	if err != nil {
		t.Fatal(err)
	}
	callID, name := savedSA.CompletionID, "agent"
	if err := parent.AppendTurn([]transcript.Message{{Role: transcript.RoleTool, Content: "{}", ToolCallID: &callID, Name: &name, IsNotify: true}}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{saved.ID, lost.ID} {
		_ = rt.transcripts.UpdateMeta(ws, id, func(m *transcript.Meta) error { m.Subagent.Reported = false; return nil })
	}

	again := WithKeyStore(rt.dataDir, config.NewConfig(rt.dataDir), config.NewMemoryKeyStore())
	t.Cleanup(func() { again.ShutdownTurns(5 * time.Second) })
	notices := again.subagents.notices(parentID, ws)()
	if len(notices) != 1 || !strings.HasPrefix(notices[0].CallID, lost.ID+"#") {
		t.Fatalf("after restart got %+v, want only %s's result", notices, lost.ID)
	}
	if !loadChild(t, again, ws, saved.ID).Meta.Subagent.Reported {
		t.Fatal("the saved result should be marked reported on restore")
	}
}

// Admission is serialized per parent: one parent spawning does not
// hold up another.
func TestSpawnAdmissionIsPerParent(t *testing.T) {
	rt, ws, parentA, _ := agentRuntime(t, func(c modelCall) modelReply { return modelReply{Text: "ok"} })
	parentB, err := rt.OpenSession("", ws)
	if err != nil {
		t.Fatal(err)
	}
	runParent(t, rt, parentA, ws, "hi")
	runParent(t, rt, parentB.SessionID, ws, "hi")
	gate := rt.subagents.admission(parentA)
	gate.Lock()
	defer gate.Unlock()
	done := make(chan error, 1)
	go func() {
		_, err := rt.subagents.spawn(parentB.SessionID, spawnArgs{Kind: subagentKinds[0], Task: "x", ForkTurns: ForkNone})
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("parent B's spawn waited on parent A's admission")
	}
}

func TestMarkReportedSurfacesWriteFailures(t *testing.T) {
	rt, ws, _, _ := agentRuntime(t, func(c modelCall) modelReply { return modelReply{Text: "ok"} })
	if err := rt.subagents.markReported(ws, []string{"00000000-0000-4000-8000-000000000000"}); err == nil {
		t.Fatal("marking a missing child reported should fail")
	}
}
