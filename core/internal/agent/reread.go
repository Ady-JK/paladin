// reread.go guards against one narrow waste: reading the exact same file
// with the exact same arguments three times back to back, with nothing in
// between, while the first result is provably still inside the model's
// live window (compaction never removed it, and nothing reset the
// streak). The third call is refused with guidance; the model still sees
// the first result, so re-reading can only return what it already has.
//
// This is deliberately not the old doom guard. That one refused any
// repeated tool call of any kind and cycles between different calls;
// this one covers a single case and leaves everything else unlimited
// (see explore_test.go, which still pins free exploration). The streak
// resets on the first sign that a re-read could be meaningful:
//
//   - any round containing another kind of call (write, edit, shell,
//     ask, browser, ...) — the file may have changed;
//   - any non-identical call, even another read of a different file —
//     the model is exploring, not stuck;
//   - compaction — the window was rebuilt and the count no longer
//     refers to what the model can still see;
//   - a steer or notify user message arriving mid-turn.
//
// Offsets and limits are part of the call's identity, so reading a
// different slice of the same file is a different call and never
// refused.

package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Ady-JK/paladin/core/internal/inference"
	"github.com/Ady-JK/paladin/core/internal/jsonx"
)

// rereadLimit is how many identical back-to-back read rounds run before
// the next one is refused. Three gives the model a chance to self-
// correct on the second try before the guard intervenes.
const rereadLimit = 3

// rereadGuard is per-turn state (Agent state is recreated each turn, so
// nothing survives between turns).
type rereadGuard struct {
	// sig is the canonical identity of the previous round's calls, used
	// to decide whether this round is an identical repeat.
	sig string
	// count is how many identical rounds have run back to back. It is
	// zero when the previous round was not an identical repeat.
	count int
}

// noteCompaction resets the streak when the window is rebuilt: after a
// compaction the model can no longer see the earlier results this
// count refers to, so a fresh read of anything is legitimate.
func (g *rereadGuard) noteCompaction() {
	g.sig, g.count = "", 0
}

// noteUserMessage resets the streak when the user steers mid-turn or a
// notify lands: the conversation changed, so "back to back" is over.
func (g *rereadGuard) noteUserMessage() {
	g.sig, g.count = "", 0
}

// noteRound classifies one round's calls. A round is an identical
// repeat only when every call is a read and every call has the same
// canonical signature as the previous round's calls, in the same order.
// Returns true when this round is the rereadLimit-th such repeat and
// must be refused.
func (g *rereadGuard) noteRound(calls []inference.ToolCall) bool {
	if len(calls) == 0 || !allReads(calls) {
		g.sig, g.count = "", 0
		return false
	}
	sig := roundSignature(calls)
	if sig != g.sig {
		g.sig, g.count = sig, 1
		return false
	}
	g.count++
	return g.count >= rereadLimit
}

func allReads(calls []inference.ToolCall) bool {
	for _, call := range calls {
		if call.Name != "read" {
			return false
		}
	}
	return true
}

func roundSignature(calls []inference.ToolCall) string {
	parts := make([]string, len(calls))
	for i, call := range calls {
		parts[i] = call.Name + "\x00" + canonicalJSON(call.Arguments)
	}
	return strings.Join(parts, "\x01")
}

// rereadRefusal is the tool result a refused round returns. It names
// what the model should do instead of merely saying no.
func rereadRefusal() string {
	return fmt.Sprintf("refused: the last %d rounds re-read the same files with identical arguments "+
		"(same path, offset and limit). The earlier results are still in your context and nothing has changed "+
		"the files in between, so another identical read returns what you already have. Use the results you have; "+
		"re-read only after you edit the file, or with a different offset/limit to see another slice.", rereadLimit)
}

func canonicalJSON(args string) string {
	var v any
	if err := json.Unmarshal([]byte(args), &v); err != nil {
		return strings.TrimSpace(args)
	}
	return string(jsonx.MustMarshal(v))
}
