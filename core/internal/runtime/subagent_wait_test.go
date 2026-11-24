package runtime

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A wait on one child must still hand over a sibling that finished first:
// its result is taken from the mailbox on the first pass, so it has to
// survive until the answer is returned.
func TestWaitKeepsASiblingResultTakenBeforeTheTargetFinished(t *testing.T) {
	release := make(chan struct{})
	rt, ws, parentID, _ := agentRuntime(t, func(c modelCall) modelReply {
		if c.Child && strings.Contains(strings.Join(c.Users, " "), "slow task") {
			select {
			case <-release:
			case <-c.Ctx.Done():
			}
			return modelReply{Text: "slow result"}
		}
		if c.Child {
			return modelReply{Text: "fast result"}
		}
		return modelReply{Text: "ok"}
	})
	runParent(t, rt, parentID, ws, "hi")
	slow, err := rt.subagents.spawn(parentID, spawnArgs{Kind: subagentKinds[0], Task: "slow task", ForkTurns: ForkNone})
	if err != nil {
		t.Fatal(err)
	}
	fast, err := rt.subagents.spawn(parentID, spawnArgs{Kind: subagentKinds[0], Task: "fast task", ForkTurns: ForkNone})
	if err != nil {
		t.Fatal(err)
	}
	rt.hub.WaitIdle(fast.ID, 5*time.Second)

	type answer struct {
		res waitResult
		err error
	}
	got := make(chan answer, 1)
	go func() {
		res, err := rt.subagents.wait(context.Background(), parentID, slow.ID, 20*time.Second)
		got <- answer{res, err}
	}()
	// The first pass takes the fast child's result; only then let the slow one finish.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		rt.subagents.mu.Lock()
		n := len(rt.subagents.pending[parentID])
		rt.subagents.mu.Unlock()
		if n == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(release)
	a := <-got
	if a.err != nil {
		t.Fatal(a.err)
	}
	var fastResult string
	for _, info := range a.res.Agents {
		if info.Name == fast.Name {
			fastResult = info.Result
		}
	}
	if !strings.Contains(fastResult, "fast result") {
		t.Fatalf("the sibling's result was lost: %+v", a.res.Agents)
	}
}
