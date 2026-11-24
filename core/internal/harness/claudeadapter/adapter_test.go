package claudeadapter

import (
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/claude"
)

func frame(s string) claude.Raw { return claude.Decode([]byte(s)) }
func TestStreamingReconcilesCompleteMessages(t *testing.T) {
	p := NewProjection()
	p.Apply(frame(`{"type":"stream_event","event":{"type":"message_start","message":{"id":"m1"}}}`))
	p.Apply(frame(`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello "}}}`))
	p.Apply(frame(`{"type":"assistant","uuid":"a1","message":{"id":"m1","content":[{"type":"text","text":"hello world"}]}}`))
	p.Apply(frame(`{"type":"assistant","uuid":"a1","message":{"id":"m1","content":[{"type":"text","text":"hello world"}]}}`))
	p.Flush(nil)
	if p.Reply.String() != "hello world" || len(p.Records) != 1 {
		t.Fatalf("duplicate reply=%q rows=%d", p.Reply.String(), len(p.Records))
	}
}
func TestThinkingToolsAndSnapshots(t *testing.T) {
	p := NewProjection()
	p.Apply(frame(`{"type":"assistant","uuid":"a1","message":{"id":"m1","content":[{"type":"thinking","thinking":"consider this"},{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":"/tmp/a","old_string":"old","new_string":"new"}}]}}`))
	snap := p.Snapshot()
	if len(snap) != 1 || snap[0].Reasoning != "consider this" || snap[0].ToolCalls == nil {
		t.Fatalf("incomplete crash snapshot %v", snap)
	}
	chunks := p.Apply(frame(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"edited"}]}}`))
	if len(chunks) != 1 || chunks[0].Tool == nil || chunks[0].Tool.Name != "edit" {
		t.Fatalf("tool not mapped: %v", chunks)
	}
	if len(p.Records) != 2 || len(p.Tools) != 0 {
		t.Fatalf("tool rows=%d pending=%d", len(p.Records), len(p.Tools))
	}
}
func TestAssistantBlocksShareAnAPIMessageID(t *testing.T) {
	p := NewProjection()
	p.Apply(frame(`{"type":"assistant","uuid":"thinking","apiBlockIndex":0,"message":{"id":"m","content":[{"type":"thinking","thinking":"consider"}]}}`))
	p.Apply(frame(`{"type":"assistant","uuid":"answer","apiBlockIndex":1,"message":{"id":"m","content":[{"type":"text","text":"answer"}]}}`))
	p.Flush(nil)
	if p.Reply.String() != "answer" || len(p.Records) != 1 || p.Records[0].Reasoning != "consider" {
		t.Fatalf("blocks lost: %#v", p.Records)
	}
}

func TestChildTextDoesNotMixIntoAnswer(t *testing.T) {
	p := NewProjection()
	p.Apply(frame(`{"type":"assistant","parent_tool_use_id":"child","message":{"id":"child1","content":[{"type":"text","text":"child answer"},{"type":"tool_use","id":"t","name":"Read","input":{"file_path":"/tmp/a"}}]}}`))
	if p.Reply.Len() != 0 || p.Tools["t"].Name != "read" {
		t.Fatal("child text contaminated parent or lost tool")
	}
}
func TestToolMappings(t *testing.T) {
	for _, tc := range []struct{ native, card string }{{"Read", "read"}, {"Write", "write"}, {"Edit", "edit"}, {"Bash", "shell"}, {"AskUserQuestion", "ask"}, {"mcp__test__read", "mcp__test__read"}} {
		card := Tool("id", tc.native, frame(`{"file_path":"/tmp/a","command":"echo ok"}`))
		if card.Name != tc.card || !strings.HasPrefix(card.Args, "{") {
			t.Fatalf("%s => %#v", tc.native, card)
		}
	}
}

// A background task is drawn as its own claude_task card, so its lifecycle
// events must not also repeat the task's command as a notice row.
func TestTaskEventsAddNoNoticeRows(t *testing.T) {
	p := NewProjection()
	for _, raw := range []string{
		`{"type":"system","subtype":"task_started","task_id":"t","description":"npm test --silent","task_type":"local_bash"}`,
		`{"type":"system","subtype":"task_progress","task_id":"t","description":"npm test --silent"}`,
		`{"type":"system","subtype":"task_updated","task_id":"t","description":"npm test --silent","patch":{"status":"completed"}}`,
		`{"type":"system","subtype":"task_notification","task_id":"t","summary":"npm test --silent finished"}`,
	} {
		if chunks := p.Apply(frame(raw)); len(chunks) != 0 {
			t.Fatalf("%s produced %d chunks, want none", raw, len(chunks))
		}
	}
	chunks := p.Apply(frame(`{"type":"system","subtype":"permission_denied","description":"Bash was denied"}`))
	if len(chunks) != 1 || chunks[0].Notice == nil {
		t.Fatalf("a denied permission must still surface: %v", chunks)
	}
}
