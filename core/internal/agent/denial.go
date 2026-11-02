// denial.go breaks approval-deny loops: the model retrying calls the
// user (or a permission rule) keeps refusing, with slightly different
// arguments each time so the identical-call guard never sees it. Three
// consecutive denials end the turn with a resumable error instead of
// pestering the user forever. Like the edit streak, this is per-turn
// state on the Agent.

package agent

import (
	"strings"

	"github.com/Ady-JK/paladin/core/internal/tools"
)

// maxConsecutiveDenials ends a turn stuck re-issuing refused calls.
// Matches Codex's denial circuit breaker (3 consecutive).
const maxConsecutiveDenials = 3

// noteDenial tracks one tool result, reporting whether the turn must
// end: three denials in a row with no other result between them. Any
// non-denial result resets the streak, so try → denied → narrower try
// → approved keeps working.
func (a *Agent) noteDenial(result *tools.ToolResult) bool {
	if !isDenial(result) {
		a.deniedStreak = 0
		return false
	}
	a.deniedStreak++
	return a.deniedStreak >= maxConsecutiveDenials
}

// isDenial reports whether a result is a permission-rule block or a
// user decline. Both carry fixed refusal copy from the gate.
func isDenial(result *tools.ToolResult) bool {
	if result == nil || !result.IsError {
		return false
	}
	return strings.Contains(result.Content, "blocked by a permission rule") ||
		strings.Contains(result.Content, "denied by the user")
}
