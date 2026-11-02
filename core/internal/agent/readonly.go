// readonly.go breaks search/read spirals where every round only
// reads: greps and reads with slightly varied patterns that the
// identical-call guard never sees, no writes, commands, or questions
// in between. After readOnlyRoundLimit such rounds the round's calls
// are refused with guidance (the turn continues — the model must act
// on what it found, ask, or answer). Any round containing another
// kind of call resets the streak, so research-then-act flows and
// batched exploration with periodic writes are unaffected.

package agent

import (
	"strconv"

	"github.com/Ady-JK/paladin/core/internal/inference"
)

// readOnlyRoundLimit is how many consecutive search/read-only rounds
// run before the next such round is refused.
const readOnlyRoundLimit = 8

// readOnlyTools are the calls that gather without acting.
var readOnlyTools = map[string]bool{
	"read": true, "grep": true, "glob": true, "list": true, "web_fetch": true,
}

// noteReadOnlyRound tracks one round's calls, reporting whether the
// round must be refused for searching without acting.
func (a *Agent) noteReadOnlyRound(calls []inference.ToolCall) bool {
	for _, call := range calls {
		if !readOnlyTools[call.Name] {
			a.readOnlyRounds = 0
			return false
		}
	}
	a.readOnlyRounds++
	return a.readOnlyRounds >= readOnlyRoundLimit
}

// readOnlyRefusal explains a refused search-only round.
func readOnlyRefusal(rounds int) string {
	return "refused: this is search/read-only round " + strconv.Itoa(rounds) + " in a row with no writes, commands, or questions in between. " +
		"Summarize what you learned and either act on it (edit, shell, ask) or answer — " +
		"do not search or re-read again until you have."
}
