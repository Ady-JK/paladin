// editstreak_test.go covers the same-file edit-loop escalation: the
// third consecutive unproductive edit/write to one path carries a
// redirect in the result itself, while productive edits and shell
// verification runs reset the streak.

package agent

import (
	"strconv"
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/inference"
	"github.com/Ady-JK/paladin/core/internal/tools"
)

func editCall(path string) *inference.ToolCall {
	return &inference.ToolCall{ID: "c1", Name: "edit", Arguments: `{"path":` + strconv.Quote(path) + `,"old_string":"x","new_string":"y"}`}
}

func editResultErr() *tools.ToolResult {
	return &tools.ToolResult{CallID: "c1", Name: "edit", IsError: true, Content: `{"error":"edit: old text not found"}`}
}

func editResultDiag() *tools.ToolResult {
	return &tools.ToolResult{CallID: "c1", Name: "edit", Content: `{"replacements":1,"diagnostics":[{"line":1,"message":"boom"}],"errors":1}`}
}

func editResultClean() *tools.ToolResult {
	return &tools.ToolResult{CallID: "c1", Name: "edit", Content: `{"replacements":1}`}
}

func TestEditStreakWarnsOnThirdFailure(t *testing.T) {
	a := &Agent{}
	for i := 0; i < 2; i++ {
		res := editResultErr()
		a.noteEditResult(editCall("/w/a.go"), res)
		if strings.Contains(res.Content, "loop-guard") {
			t.Fatalf("round %d warned too early: %s", i+1, res.Content)
		}
	}
	res := editResultErr()
	a.noteEditResult(editCall("/w/a.go"), res)
	if !strings.Contains(res.Content, "loop-guard") || !strings.Contains(res.Content, "/w/a.go") {
		t.Fatalf("third failure must escalate: %s", res.Content)
	}
}

func TestEditStreakCountsDiagnostics(t *testing.T) {
	a := &Agent{}
	for i := 0; i < 3; i++ {
		res := editResultDiag()
		a.noteEditResult(editCall("/w/b.go"), res)
		if i < 2 && strings.Contains(res.Content, "loop-guard") {
			t.Fatalf("round %d warned too early", i+1)
		}
		if i == 2 && !strings.Contains(res.Content, "loop-guard") {
			t.Fatalf("diagnostics streak must escalate: %s", res.Content)
		}
	}
}

func TestEditStreakResetsOnProgress(t *testing.T) {
	a := &Agent{}
	a.noteEditResult(editCall("/w/c.go"), editResultErr())
	a.noteEditResult(editCall("/w/c.go"), editResultErr())
	// A clean edit to the same path resets the streak.
	a.noteEditResult(editCall("/w/c.go"), editResultClean())
	res := editResultErr()
	a.noteEditResult(editCall("/w/c.go"), res)
	if strings.Contains(res.Content, "loop-guard") {
		t.Fatalf("streak should have reset: %s", res.Content)
	}
	// A successful shell run (verification, different work) clears all.
	a.noteEditResult(editCall("/w/c.go"), editResultErr())
	a.noteEditResult(editCall("/w/c.go"), editResultErr())
	shellOK := &tools.ToolResult{CallID: "s1", Name: "shell", Content: `{"exit_code":0}`}
	a.noteEditResult(&inference.ToolCall{ID: "s1", Name: "shell", Arguments: `{}`}, shellOK)
	res = editResultErr()
	a.noteEditResult(editCall("/w/c.go"), res)
	if strings.Contains(res.Content, "loop-guard") {
		t.Fatalf("shell success should clear streaks: %s", res.Content)
	}
}

func TestEditStreakIgnoresOtherTools(t *testing.T) {
	a := &Agent{}
	a.noteEditResult(editCall("/w/d.go"), editResultErr())
	r := &tools.ToolResult{CallID: "r1", Name: "read", Content: `{"content":"..."}`}
	a.noteEditResult(&inference.ToolCall{ID: "r1", Name: "read", Arguments: `{}`}, r)
	res := editResultErr()
	a.noteEditResult(editCall("/w/d.go"), res)
	if strings.Contains(res.Content, "loop-guard") {
		t.Fatalf("a read must not reset the streak, want warning on 3rd: %s", res.Content)
	}
	res = editResultErr()
	a.noteEditResult(editCall("/w/d.go"), res)
	if !strings.Contains(res.Content, "loop-guard") {
		t.Fatalf("third consecutive failure must escalate: %s", res.Content)
	}
}
