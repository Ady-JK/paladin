package agent

import (
	"encoding/json"
	"fmt"

	"github.com/Ady-JK/paladin/core/internal/jsonx"
)

// doomLimit is how many consecutive identical calls trip the guard.
const doomLimit = 3

// doomGuard catches a model stuck re-issuing the same call: the result
// cannot change, so the call is refused with guidance instead of being
// run again and burning rounds.
type doomGuard struct {
	last  string
	count int
}

// check records the call and returns a refusal message when it is the
// doomLimit-th identical call in a row, or "" when it may run.
func (g *doomGuard) check(name, args string) string {
	if isPolling(name, args) {
		g.last, g.count = "", 0
		return ""
	}
	sig := name + "\x00" + canonicalArgs(args)
	if sig == g.last {
		g.count++
	} else {
		g.last, g.count = sig, 1
	}
	if g.count < doomLimit {
		return ""
	}
	return fmt.Sprintf("refused: %s was called with identical arguments %d times in a row (repeated call); the result will not change. Use a different approach, different arguments, or report what is blocking you.", name, g.count)
}

// isPolling exempts calls whose result legitimately changes between
// identical invocations.
func isPolling(name, args string) bool {
	if name != "shell" {
		return false
	}
	var a struct {
		Action string `json:"action"`
	}
	_ = json.Unmarshal([]byte(args), &a)
	return a.Action == "status"
}

func canonicalArgs(args string) string {
	var v any
	if err := json.Unmarshal([]byte(args), &v); err != nil {
		return args
	}
	return string(jsonx.MustMarshal(v))
}
