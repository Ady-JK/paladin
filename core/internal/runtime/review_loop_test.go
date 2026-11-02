// review_loop_test.go covers the runtime half of the memory review loop:
// the turn counter advancing on real turns, and the fork appearing with a
// memory-only tool set when memory.json opts in.

package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Ady-JK/paladin/core/internal/memory"
)

// writeMemoryConfig points the runtime at a memory.json with the given
// text and reloads it.
func writeMemoryConfig(t *testing.T, rt *AgentRuntime, text string) {
	t.Helper()
	path := memory.ConfigPath(rt.dataDir)
	if err := writeFileMkdir(path, text); err != nil {
		t.Fatal(err)
	}
	if err := rt.ReloadMemoryConfig(); err != nil {
		t.Fatal(err)
	}
}

func TestFinishedTurnsAdvanceTheReviewCounter(t *testing.T) {
	rt, ws, parentID, _ := agentRuntime(t, func(modelCall) modelReply {
		return modelReply{Text: "done"}
	})
	runParent(t, rt, parentID, ws, "first")
	runParent(t, rt, parentID, ws, "second")
	rt.hub.SettleDone(5 * time.Second)

	sess, err := rt.transcripts.Load(ws, parentID)
	if err != nil {
		t.Fatal(err)
	}
	if st := memory.LoadReviewState(sess.Dir); st.TurnsSinceWrite != 2 {
		t.Fatalf("turn counter = %+v", st)
	}
}

func TestMemoryWriteDuringATurnResetsTheCounter(t *testing.T) {
	rt, ws, parentID, _ := agentRuntime(t, func(c modelCall) modelReply {
		if _, ok := c.Results["memory"]; ok {
			return modelReply{Text: "saved"}
		}
		if len(c.Results) == 0 && strings.Contains(c.Users[len(c.Users)-1], "remember") {
			return modelReply{Calls: []mockToolCall{call("memory", map[string]any{
				"target":     "project",
				"operations": []any{map[string]any{"action": "add", "content": "tests run with make test"}},
			})}}
		}
		return modelReply{Text: "noted"}
	})
	runParent(t, rt, parentID, ws, "warm up")
	runParent(t, rt, parentID, ws, "remember this")
	rt.hub.SettleDone(5 * time.Second)

	sess, err := rt.transcripts.Load(ws, parentID)
	if err != nil {
		t.Fatal(err)
	}
	st := memory.LoadReviewState(sess.Dir)
	if st.TurnsSinceWrite != 0 {
		t.Fatalf("a memory write must reset the counter: %+v", st)
	}
	res, err := rt.memory.Read(memory.TargetProject, ws)
	if err != nil || len(res.Entries) != 1 {
		t.Fatalf("entry not saved: %+v %v", res, err)
	}
}

func TestAutoReviewForksAMemoryOnlyChild(t *testing.T) {
	rt, ws, parentID, _ := agentRuntime(t, func(c modelCall) modelReply {
		if !c.Child {
			return modelReply{Text: "work done"}
		}
		// The child is the review fork: save one fact through the only
		// write tool it has.
		if _, ok := c.Results["memory"]; ok {
			return modelReply{Text: "saved one fact"}
		}
		return modelReply{Calls: []mockToolCall{call("memory", map[string]any{
			"target":     "global",
			"operations": []any{map[string]any{"action": "add", "content": "reviews run in a fork"}},
		})}}
	})
	writeMemoryConfig(t, rt, `{"autoReview": true}`)
	setLimits(t, rt, 2, 2)

	runParent(t, rt, parentID, ws, "do some work")
	rt.hub.SettleDone(5 * time.Second)

	ids := childIDs(t, rt, ws, parentID)
	if len(ids) != 1 {
		t.Fatalf("review child not spawned: %v", ids)
	}
	// The fork is queued after the parent's turn, so wait for its own
	// turn to settle before reading what it wrote.
	sa := childSettled(t, rt, ws, ids[0])
	if sa.Type != "memory-review" || sa.ForkTurns != ForkAll {
		t.Fatalf("review child meta = %+v", sa)
	}
	res, err := rt.memory.Read(memory.TargetGlobal, ws)
	if err != nil || len(res.Entries) != 1 || res.Entries[0] != "reviews run in a fork" {
		t.Fatalf("review did not save memory: %+v %v", res, err)
	}
}

func TestReviewChildCannotReachWorkspaceTools(t *testing.T) {
	rt, ws, parentID, _ := agentRuntime(t, func(c modelCall) modelReply {
		return modelReply{Text: "nothing"}
	})
	writeMemoryConfig(t, rt, `{"autoReview": true}`)
	setLimits(t, rt, 2, 2)

	runParent(t, rt, parentID, ws, "work")
	rt.hub.SettleDone(5 * time.Second)
	ids := childIDs(t, rt, ws, parentID)
	if len(ids) != 1 {
		t.Fatalf("review child not spawned: %v", ids)
	}
	child := loadChild(t, rt, ws, ids[0])
	reg := rt.childTools(child, reviewKind())
	for _, banned := range []string{"write", "edit", "shell", "spawn_agent", "browser"} {
		if _, ok := reg.Get(banned); ok {
			t.Fatalf("review child was given %q; it may only write memory", banned)
		}
	}
	// The model-facing name is session_search's own; the child's memory
	// tool writes through the same bank, so assert on the kind allowlist
	// rather than the registry name.
	if !reviewKind().allows("memory") {
		t.Fatal("review child cannot write memory")
	}
}

func TestAutoReviewOffDoesNotFork(t *testing.T) {
	rt, ws, parentID, calls := agentRuntime(t, func(modelCall) modelReply {
		return modelReply{Text: "work done"}
	})
	setLimits(t, rt, 2, 2)
	runParent(t, rt, parentID, ws, "do some work")
	rt.hub.SettleDone(5 * time.Second)

	if ids := childIDs(t, rt, ws, parentID); len(ids) != 0 {
		t.Fatalf("forked a review with autoReview off: %v", ids)
	}
	if n := len(calls()); n != 1 {
		t.Fatalf("extra model requests with autoReview off: %d", n)
	}
}

func TestReviewKindIsNotADelegationTarget(t *testing.T) {
	names := subagentKindNames()
	for _, n := range names {
		if n == "memory-review" {
			t.Fatal("the review fork must not be offered to the model as a spawn target")
		}
	}
	if _, ok := findSubagentKind("memory-review"); !ok {
		t.Fatal("the runtime still needs the kind by name")
	}
	if !strings.Contains(reviewKind().prompt(), "memory tool") {
		t.Fatal("review role does not tell the child to use the memory tool")
	}
}

// writeFileMkdir writes text, creating the parent directory.
func writeFileMkdir(path, text string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(text), 0o600)
}
