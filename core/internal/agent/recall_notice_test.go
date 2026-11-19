package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/inference"
	"github.com/Ady-JK/paladin/core/internal/memory"
)

func TestTransientNoticeReachesTheUserButNotTheConversation(t *testing.T) {
	prov := &mockProvider{streams: []streamScript{{chunks: []inference.Chunk{inference.TextChunk("ok")}}}}
	a, _, tx, done := testAgent(t, prov)
	defer close(done)
	sent := false
	a.notices = func() []Notice {
		if sent {
			return nil
		}
		sent = true
		return []Notice{{CallID: "skip-1", Name: "memory", Content: "already in the conversation", Transient: true}}
	}
	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	var shown bool
	for _, c := range drainChunks(tx) {
		if c.Notice != nil && c.Notice.Kind == "info" && c.Notice.Text == "already in the conversation" {
			shown = true
		}
		if c.Tool != nil && c.Tool.Name == "memory" {
			t.Fatalf("a transient notice became a tool row: %+v", c.Tool)
		}
	}
	if !shown {
		t.Fatal("the user was not told")
	}
	for _, m := range a.messages {
		if m.Content == "already in the conversation" {
			t.Fatal("a transient notice joined the conversation")
		}
	}
}

func TestAppendedRunsOnlyForNoticesThatJoinTheConversation(t *testing.T) {
	prov := &mockProvider{streams: []streamScript{{chunks: []inference.Chunk{inference.TextChunk("ok")}}}}
	a, _, _, done := testAgent(t, prov)
	defer close(done)
	appended := 0
	sent := false
	a.notices = func() []Notice {
		if sent {
			return nil
		}
		sent = true
		return []Notice{{CallID: "r-1", Name: "memory", Content: "recalled", Appended: func() { appended++ }}}
	}
	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	if appended != 1 {
		t.Fatalf("Appended ran %d times", appended)
	}
}

func TestCompactionForgetsRecalledMemoryItSummarizedAway(t *testing.T) {
	sess := testSession(t, "00000000-0000-4000-8000-000000000073")
	hash := memory.RecallHash(memory.TargetProject, "uses pnpm workspaces")
	if err := memory.MarkRecalled(sess.Dir, hash); err != nil {
		t.Fatal(err)
	}
	prov := &mockProvider{streams: bigReadRounds(t, sess.Meta.Cwd, 6), complete: inference.ToolResponse{Content: "SUM"}}
	a, _, done := newAgentForTest(t, prov, sess, "go", false, true)
	defer close(done)
	a.window = 1000
	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	if len(prov.completeMsgs) == 0 {
		t.Fatal("the turn never compacted")
	}
	if _, err := os.Stat(filepath.Join(sess.Dir, memory.RecalledFile)); !os.IsNotExist(err) {
		t.Fatalf("recalled hashes survived the compaction: %v", err)
	}
	if memory.IsRecalled(sess.Dir, hash) {
		t.Fatal("the entry is still marked as recalled")
	}
}

// A compaction keeps a tail of the conversation. An entry whose reminder is
// in that tail is still in front of the model, so it must not be recalled
// again; one whose reminder was summarized away may be.
func TestCompactionKeepsEntriesWhoseReminderSurvivesInTheTail(t *testing.T) {
	sess := testSession(t, "00000000-0000-4000-8000-000000000074")
	prov := &mockProvider{complete: inference.ToolResponse{Content: "SUM"}}
	a, _, done := newAgentForTest(t, prov, sess, "go", false, true)
	defer close(done)

	const oldEntry, tailEntry = "old entry that gets summarized", "recent entry kept in the tail"
	oldHash := memory.RecallHash(memory.TargetProject, oldEntry)
	tailHash := memory.RecallHash(memory.TargetProject, tailEntry)
	for _, h := range []string{oldHash, tailHash} {
		if err := memory.MarkRecalled(sess.Dir, h); err != nil {
			t.Fatal(err)
		}
	}
	filler := strings.Repeat("x", 4000)
	msgs := []inference.LlmMessage{{Role: "user", Content: memory.RecallNote(memory.TargetProject, oldEntry)}}
	for i := 0; i < 8; i++ {
		msgs = append(msgs,
			inference.LlmMessage{Role: "user", Content: filler},
			inference.LlmMessage{Role: "assistant", Content: filler})
	}
	msgs = append(msgs,
		inference.LlmMessage{Role: "user", Content: memory.RecallNote(memory.TargetProject, tailEntry)},
		inference.LlmMessage{Role: "assistant", Content: "noted"})
	a.messages = msgs
	a.window = 1000

	if err := a.compact("manual", a.estimateContext()); err != nil {
		t.Fatal(err)
	}
	if memory.IsRecalled(sess.Dir, oldHash) {
		t.Fatal("an entry whose reminder was summarized away is still marked as recalled")
	}
	if !memory.IsRecalled(sess.Dir, tailHash) {
		t.Fatal("an entry whose reminder is still in the tail would be appended a second time")
	}
}
