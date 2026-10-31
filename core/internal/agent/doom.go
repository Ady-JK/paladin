package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Ady-JK/paladin/core/internal/jsonx"
)

// doomLimit is how many back-to-back laps of the same call sequence
// trip the guard: three identical calls, or a two-to-four call cycle
// (A,B,A,B,A,B) repeated three times.
const (
	doomLimit     = 3
	doomMaxPeriod = 4
)

// doomGuard catches a model stuck re-issuing the same call or cycling
// through the same few calls: the results cannot change, so the call is
// refused with guidance instead of being run again. It never ends the
// turn; the model gets the refusal as the tool result and must adapt.
type doomGuard struct {
	recent []doomCall
}

type doomCall struct{ name, sig string }

// check records the call and returns a refusal message when it
// completes the doomLimit-th lap of a repeating sequence, or "" when it
// may run.
func (g *doomGuard) check(name, args string) string {
	if isDynamicAction(name, args) {
		g.recent = g.recent[:0]
		return ""
	}
	g.recent = append(g.recent, doomCall{name: name, sig: name + "\x00" + canonicalArgs(args)})
	if keep := doomLimit * doomMaxPeriod; len(g.recent) > keep {
		g.recent = append(g.recent[:0], g.recent[len(g.recent)-keep:]...)
	}
	for period := 1; period <= doomMaxPeriod; period++ {
		if !g.repeats(period) {
			continue
		}
		if period == 1 {
			return fmt.Sprintf("refused: %s was called with identical arguments %d times in a row (repeated call); the result will not change. Use a different approach, different arguments, or report what is blocking you.", name, doomLimit)
		}
		lap := g.recent[len(g.recent)-period:]
		names := make([]string, len(lap))
		for i, c := range lap {
			names[i] = c.name
		}
		return fmt.Sprintf("refused: the last %d calls repeat the same cycle (%s) %d times with identical arguments; nothing changes between laps. Use a different approach, change something before retrying, or report what is blocking you.", period*doomLimit, strings.Join(names, " → "), doomLimit)
	}
	return ""
}

// repeats reports whether the newest doomLimit*period calls are one
// period-long block repeated doomLimit times. Blocks of a single
// repeated call are left to period 1.
func (g *doomGuard) repeats(period int) bool {
	n := len(g.recent)
	if n < period*doomLimit {
		return false
	}
	tail := g.recent[n-period*doomLimit:]
	for i := period; i < len(tail); i++ {
		if tail[i].sig != tail[i-period].sig {
			return false
		}
	}
	if period == 1 {
		return true
	}
	for i := 1; i < period; i++ {
		if tail[i].sig != tail[0].sig {
			return true
		}
	}
	return false
}

// isDynamicAction exempts calls whose result or page state can change
// between identical invocations. Browser clicks and other interactions
// can intentionally repeat on the same locator.
func isDynamicAction(name, args string) bool {
	if name == "browser" {
		return true
	}
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
