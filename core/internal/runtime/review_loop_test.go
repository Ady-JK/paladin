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

func TestMemoryReviewReminderPreservesSystemPromptAcrossTurns(t *testing.T) {
	var systems []string
	var users [][]string
	rt, ws, parentID, _ := agentRuntime(t, func(c modelCall) modelReply {
		if !c.Child {
			systems = append(systems, c.System)
			users = append(users, append([]string(nil), c.Users...))
		}
		return modelReply{Text: "done"}
	})
	writeMemoryConfig(t, rt, `{"reviewTurns": 1}`)
	runParent(t, rt, parentID, ws, "first task")
	rt.hub.SettleDone(5 * time.Second)
	runParent(t, rt, parentID, ws, "second task")
	rt.hub.SettleDone(5 * time.Second)
	sess, err := rt.transcripts.Load(ws, parentID)
	if err != nil {
		t.Fatal(err)
	}
	if err := memory.NoteWrite(sess.Dir); err != nil {
		t.Fatal(err)
	}
	if _, err := memory.EndTurn(sess.Dir); err != nil {
		t.Fatal(err)
	}
	runParent(t, rt, parentID, ws, "third task")
	if len(systems) != 3 || systems[0] != systems[1] || systems[1] != systems[2] {
		t.Fatal("memory review changed the cached system prompt between turns")
	}
	if !strings.Contains(strings.Join(users[1], "\n"), "MEMORY REVIEW:") {
		t.Fatal("due memory review reminder was not sent to the model")
	}
	if len(users[2]) == 0 || users[2][len(users[2])-1] != "third task" {
		t.Fatal("memory review reminder remained due after a memory write")
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
	reg := rt.childToolsFrom(child, reviewKind(), rt.tools)
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
	// Inspect the real model-facing schema instead of a parallel helper list.
	def := spawnDefFor(subagentKinds)
	props := def.Parameters.(map[string]any)["properties"].(map[string]any)
	names := props["agent_type"].(map[string]any)["enum"].([]string)
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

// TestRecallAddsOneReminderForAMatchingMessage pins the injection point:
// a message that clearly matches a stored entry carries that entry as a
// short reminder in the model request, and an unrelated message does not
// grow the prompt at all.
func TestRecallAddsOneReminderForAMatchingMessage(t *testing.T) {
	var last []string // the newest user message of each model request
	rt, ws, parentID, _ := agentRuntime(t, func(c modelCall) modelReply {
		if !c.Child && len(c.Users) > 0 {
			last = append(last, c.Users[len(c.Users)-1])
		}
		return modelReply{Text: "ok"}
	})
	res, err := rt.memory.Apply(memory.TargetProject, ws, []memory.Op{
		{Action: "add", Content: "tests run with make test, which drives playwright on port 5173"},
	})
	if err != nil || len(res.Entries) != 1 {
		t.Fatalf("seed memory: %+v %v", res, err)
	}

	runParent(t, rt, parentID, ws, "how do I run the playwright tests?")
	rt.hub.SettleDone(5 * time.Second)
	runParent(t, rt, parentID, ws, "unrelated question about the weather")

	if len(last) < 2 {
		t.Fatalf("model requests = %d", len(last))
	}
	if !strings.Contains(last[0], "Recalled from project memory") || !strings.Contains(last[0], "playwright") {
		t.Fatalf("matching message got no reminder:\n%s", last[0])
	}
	if strings.Contains(last[1], "Recalled from") {
		t.Fatalf("unrelated message was reminded:\n%s", last[1])
	}
}

// recallCount is how many recalled-memory reminders the model's last
// request carried.
func recallCount(calls []modelCall) int {
	var last modelCall
	for _, c := range calls {
		if !c.Child {
			last = c
		}
	}
	n := 0
	for _, u := range last.Users {
		if strings.Contains(u, "Recalled from project memory") {
			n++
		}
	}
	return n
}

func TestARecalledEntryIsAppendedOnceUntilTheConversationForgetsIt(t *testing.T) {
	rt, ws, sid, calls := agentRuntime(t, func(modelCall) modelReply { return modelReply{Text: "done"} })
	if _, err := rt.memory.Apply(memory.TargetProject, ws, []memory.Op{{Action: "add", Content: "desktop playwright tests run with pnpm exec playwright test"}}); err != nil {
		t.Fatal(err)
	}
	runParent(t, rt, sid, ws, "run the desktop playwright tests")
	if n := recallCount(calls()); n != 1 {
		t.Fatalf("first match recalled %d times, want 1", n)
	}
	runParent(t, rt, sid, ws, "rerun the playwright tests for desktop")
	if n := recallCount(calls()); n != 1 {
		t.Fatalf("a repeat match appended the entry again: %d copies in the conversation", n)
	}

	// Once the conversation no longer holds the reminder, the entry is
	// recalled afresh.
	sess, err := rt.transcripts.Load(ws, sid)
	if err != nil {
		t.Fatal(err)
	}
	if err := memory.ResetRecalled(sess.Dir, nil); err != nil {
		t.Fatal(err)
	}
	runParent(t, rt, sid, ws, "one more playwright desktop tests run")
	if n := recallCount(calls()); n != 2 {
		t.Fatalf("after clearing, the entry was recalled %d times in total, want 2", n)
	}
}

func TestAnEditedEntryIsRecalledAsANewOne(t *testing.T) {
	rt, ws, sid, calls := agentRuntime(t, func(modelCall) modelReply { return modelReply{Text: "done"} })
	op := func(o memory.Op) {
		t.Helper()
		if _, err := rt.memory.Apply(memory.TargetProject, ws, []memory.Op{o}); err != nil {
			t.Fatal(err)
		}
	}
	op(memory.Op{Action: "add", Content: "desktop playwright tests run with pnpm exec playwright test"})
	runParent(t, rt, sid, ws, "run the desktop playwright tests")
	op(memory.Op{Action: "patch", OldText: "desktop playwright tests run", Content: "desktop playwright tests run headless with pnpm exec playwright test"})
	runParent(t, rt, sid, ws, "run the desktop playwright tests again")
	if n := recallCount(calls()); n != 2 {
		t.Fatalf("an edited entry should be recalled again, got %d reminders", n)
	}
}
