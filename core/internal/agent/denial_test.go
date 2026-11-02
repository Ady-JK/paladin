// denial_test.go covers the consecutive-denial breaker: three denied
// tool results in a row end the turn instead of pestering on, while
// any other result resets the streak.

package agent

import (
	"testing"

	"github.com/Ady-JK/paladin/core/internal/tools"
)

func deniedResult() *tools.ToolResult {
	return &tools.ToolResult{CallID: "d1", Name: "shell", IsError: true,
		Content: `{"error":"denied by the user: they declined this shell call. Do not retry it; continue another way or ask the user what they want."}`}
}

func blockedResult() *tools.ToolResult {
	return &tools.ToolResult{CallID: "d2", Name: "shell", IsError: true,
		Content: `{"error":"blocked by a permission rule (write outside workspace). Do not retry it; choose another approach or ask the user."}`}
}

func okResult() *tools.ToolResult {
	return &tools.ToolResult{CallID: "o1", Name: "read", Content: `{"content":"hi"}`}
}

func TestDenialBreakerTripsOnThree(t *testing.T) {
	a := &Agent{}
	if a.noteDenial(deniedResult()) || a.noteDenial(blockedResult()) {
		t.Fatal("first two denials must not end the turn")
	}
	if !a.noteDenial(deniedResult()) {
		t.Fatal("third consecutive denial must end the turn")
	}
}

func TestDenialBreakerResetsOnOtherResults(t *testing.T) {
	a := &Agent{}
	a.noteDenial(deniedResult())
	a.noteDenial(deniedResult())
	if a.noteDenial(okResult()) {
		t.Fatal("a success must reset the streak")
	}
	if a.noteDenial(deniedResult()) || a.noteDenial(deniedResult()) {
		t.Fatal("streak restarted, must not trip yet")
	}
	errResult := &tools.ToolResult{CallID: "e1", Name: "read", IsError: true, Content: `{"error":"read: no such file"}`}
	if a.noteDenial(errResult) {
		t.Fatal("an ordinary error is not a denial")
	}
	// Non-error content mentioning denial words is not a denial either.
	if a.noteDenial(&tools.ToolResult{CallID: "o2", Name: "read", Content: "the user denied nothing"}) {
		t.Fatal("non-error text must not count")
	}
}
