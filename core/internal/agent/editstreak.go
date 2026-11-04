// editstreak.go breaks the dominant real-world edit loop: the model
// fixes one diagnostics error (or one stale old_string miss) per round
// on the same file, round after round, each round resending the whole
// conversation. Consecutive unproductive edit/write results on one path
// earn an escalation note inside the result itself, redirecting the
// model to batch the remaining fixes and verify with a shell
// build/test run instead of guessing. A productive edit to the path,
// or a successful shell run, clears the streak. Guidance only — the
// turn runs until the model answers, you stop it, or a guard breaks.

package agent

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/Ady-JK/paladin/core/internal/inference"
	"github.com/Ady-JK/paladin/core/internal/tools"
)

// editStreakWarnAt is how many consecutive unproductive edits to one
// path trigger the escalation note.
const editStreakWarnAt = 3

// noteEditResult tracks one tool call for the edit loop. It may append
// an escalation note to an edit/write result's content.
func (a *Agent) noteEditResult(call *inference.ToolCall, result *tools.ToolResult) {
	if call.Name == "shell" && !result.IsError {
		// A verification run or genuinely different work: the model
		// is no longer fixated on one file.
		a.editStreaks = nil
		return
	}
	if call.Name != "edit" && call.Name != "write" {
		return
	}
	path := editCallPath(call.Arguments)
	if path == "" {
		return
	}
	if !result.IsError {
		a.changed++
	}
	if a.editStreaks == nil {
		a.editStreaks = map[string]int{}
	}
	if !result.IsError && !hasEditDiagnostics(result.Content) {
		delete(a.editStreaks, path)
		return
	}
	a.editStreaks[path]++
	if n := a.editStreaks[path]; n >= editStreakWarnAt {
		result.Content += "\n\n[loop-guard] Unproductive edit #" + strconv.Itoa(n) +
			" to " + path + " in a row (failures or lingering diagnostics errors). " +
			"Stop fixing one error per round: batch ALL remaining fixes to this file " +
			"into your next edit calls, then run the build/tests with shell and fix " +
			"from their output instead of guessing."
	}
}

// editCallPath pulls the file path out of edit/write arguments.
// Best-effort: unparseable arguments simply are not tracked.
func editCallPath(args string) string {
	var a struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(args), &a); err != nil {
		return ""
	}
	return strings.TrimSpace(a.Path)
}

// hasEditDiagnostics reports whether an edit/write result still carries
// diagnostics errors: the edit applied, but the file is not clean.
func hasEditDiagnostics(content string) bool {
	var out struct {
		Diagnostics []any `json:"diagnostics"`
		Errors      int   `json:"errors"`
	}
	if err := json.Unmarshal([]byte(content), &out); err != nil {
		return false
	}
	return len(out.Diagnostics) > 0 || out.Errors > 0
}
