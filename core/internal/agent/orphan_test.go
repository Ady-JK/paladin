package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Ady-JK/paladin/core/internal/inference"
	"github.com/Ady-JK/paladin/core/internal/tools"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

func assistantCalls(ids ...string) transcript.Message {
	calls := make([]transcript.ToolCall, 0, len(ids))
	for _, id := range ids {
		calls = append(calls, transcript.ToolCall{ID: id, Name: "read", Arguments: "{}"})
	}
	return transcript.Message{Role: transcript.RoleAssistant, CreatedAt: time.Now().UTC(), ToolCalls: &calls}
}

func toolResult(id, content string) transcript.Message {
	name := "read"
	return transcript.Message{Role: transcript.RoleTool, Content: content, CreatedAt: time.Now().UTC(), ToolCallID: &id, Name: &name}
}

// assertPaired checks the provider invariant: every assistant tool call
// is answered by exactly one tool message before the next non-tool
// message, and no tool message answers an unknown or settled call.
func assertPaired(t *testing.T, msgs []inference.LlmMessage) {
	t.Helper()
	var pending map[string]bool
	flush := func(at int) {
		if len(pending) > 0 {
			t.Fatalf("message %d: unanswered tool calls %v in %+v", at, pending, msgs)
		}
		pending = nil
	}
	for i, m := range msgs {
		switch m.Role {
		case "tool":
			if m.ToolCallID == nil || !pending[*m.ToolCallID] {
				t.Fatalf("message %d: tool result without a pending call: %+v", i, m)
			}
			delete(pending, *m.ToolCallID)
		case "assistant":
			flush(i)
			pending = map[string]bool{}
			for _, c := range m.ToolCalls {
				pending[c.ID] = true
			}
		default:
			flush(i)
		}
	}
	flush(len(msgs))
}

func TestLlmMessagesSynthesizesMissingToolResults(t *testing.T) {
	history := []transcript.Message{
		transcript.UserMessage("go"),
		assistantCalls("c1", "c2", "c3"),
		toolResult("c1", "ok"),
		transcript.UserMessage("next"),
	}
	msgs := LlmMessages(history, true)
	assertPaired(t, msgs)
	var synthetic []string
	for _, m := range msgs {
		if m.Role == "tool" && strings.Contains(m.Content, "aborted") {
			synthetic = append(synthetic, *m.ToolCallID)
		}
	}
	if len(synthetic) != 2 || synthetic[0] != "c2" || synthetic[1] != "c3" {
		t.Fatalf("want aborted results for c2,c3 in call order, got %v", synthetic)
	}
	if msgs[len(msgs)-1].Role != "user" || msgs[len(msgs)-1].Content != "next" {
		t.Fatalf("the user message must follow the repaired results: %+v", msgs)
	}
}

func TestLlmMessagesTrailingOrphanCallsAreSettled(t *testing.T) {
	history := []transcript.Message{transcript.UserMessage("go"), assistantCalls("c1")}
	assertPaired(t, LlmMessages(history, true))
}

func TestLlmMessagesDropsDuplicateAndLateResults(t *testing.T) {
	history := []transcript.Message{
		transcript.UserMessage("go"),
		assistantCalls("c1"),
		toolResult("c1", "first"),
		toolResult("c1", "dup"),
		transcript.UserMessage("between"),
		toolResult("c1", "late"),
	}
	msgs := LlmMessages(history, true)
	assertPaired(t, msgs)
	for _, m := range msgs {
		if m.Role == "tool" && m.Content != "first" {
			t.Fatalf("only the first result may keep the tool role: %+v", m)
		}
	}
}

func TestLlmMessagesReusedIDsAcrossRounds(t *testing.T) {
	// Gateways without call ids fall back to pending-0 every round; each
	// round must pair with its own result, not the first round's.
	history := []transcript.Message{
		transcript.UserMessage("go"),
		assistantCalls("pending-0"),
		toolResult("pending-0", "r1"),
		assistantCalls("pending-0"),
		toolResult("pending-0", "r2"),
	}
	msgs := LlmMessages(history, true)
	assertPaired(t, msgs)
	var got []string
	for _, m := range msgs {
		if m.Role == "tool" {
			got = append(got, m.Content)
		}
	}
	if len(got) != 2 || got[0] != "r1" || got[1] != "r2" {
		t.Fatalf("got %v", got)
	}
}

// stopperTool cancels the turn while it runs, like a user pressing stop.
type stopperTool struct{ stop func() }

func (stopperTool) Name() string { return "stopper" }
func (stopperTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{Name: "stopper", Parameters: map[string]any{"type": "object"}}
}
func (s stopperTool) Call(json.RawMessage, *tools.ToolCtx) (string, error) {
	s.stop()
	return `{"ok":true}`, nil
}

func TestStopMidToolsPersistsAbortedResults(t *testing.T) {
	prov := &mockProvider{streams: []streamScript{{chunks: []inference.Chunk{{ToolCalls: []inference.ToolCall{
		{ID: "a", Name: "stopper", Arguments: "{}"},
		{ID: "b", Name: "read", Arguments: "{}"},
		{ID: "c", Name: "read", Arguments: "{}"},
	}}}}}}
	a, sess, tx, done := testAgent(t, prov, 5)
	defer close(done)
	a.tools.Register(stopperTool{stop: a.cancel})
	outcome, err := a.Run()
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.Stopped {
		t.Fatalf("want stopped, got %+v", outcome)
	}
	history, err := sess.History()
	if err != nil {
		t.Fatal(err)
	}
	results := map[string]string{}
	for _, m := range history {
		if m.Role == transcript.RoleTool && m.ToolCallID != nil {
			results[*m.ToolCallID] = m.Content
		}
	}
	for _, id := range []string{"a", "b", "c"} {
		if _, ok := results[id]; !ok {
			t.Fatalf("call %s has no persisted result: %v", id, results)
		}
	}
	if !strings.Contains(results["b"], "aborted") || !strings.Contains(results["c"], "aborted") {
		t.Fatalf("unrun calls must be settled as aborted: %v", results)
	}
	var aborted int
	for _, c := range drainChunks(tx) {
		if c.Tool != nil && c.Tool.Status == "failed" && strings.Contains(c.Tool.Result, "aborted") {
			aborted++
		}
	}
	if aborted != 2 {
		t.Fatalf("the UI must see both aborted rows settle, saw %d", aborted)
	}
	assertPaired(t, LlmMessages(history, true))
}
