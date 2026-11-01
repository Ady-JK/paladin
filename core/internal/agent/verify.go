// verify.go is the check-before-finishing nudge. When a turn is about
// to end after it changed code and no check has passed since (a build,
// test, linter or type check: see checks.go; pwd or ls do not count),
// the model gets one short reminder naming the files, then the turn
// goes on. A failed check keeps the reminder and says it failed. It fires at most once per
// turn, never when tools are off (budget wrap-up) and not for doc-only
// changes; the user can switch it off in settings.

package agent

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/Ady-JK/paladin/core/internal/custom"
	"github.com/Ady-JK/paladin/core/internal/inference"
	"github.com/Ady-JK/paladin/core/internal/tools"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

const verifyDisplay = "Check before finishing"

const (
	verifyDefault = "Before you finish: this turn changed {{files}} and no build, test or linter has passed since. " +
		"If one (or the app itself) can check these changes here, run it now and fix what fails. " +
		"Prefer the narrowest check that covers the changed files (the touched package's tests or a build of it); " +
		"run the repository-wide suite only when the change crosses packages. " +
		"If nothing applies or it cannot run here, say so in one line in your answer."
	verifyFailedDefault = "Before you finish: this turn changed {{files}} and the last check you ran failed. " +
		"Fix what fails and run it again. If the failure is unrelated to these changes or cannot be fixed here, " +
		"say so in one line in your answer."
)

// verifyMaxFiles caps the files named in the reminder.
const verifyMaxFiles = 8

// docExts are changes that need no build or test run.
var docExts = map[string]bool{
	".md": true, ".mdx": true, ".txt": true, ".rst": true, ".adoc": true,
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".svg": true, ".webp": true,
}

// noteVerify tracks code changes and runs from one finished call.
func (a *Agent) noteVerify(call *inference.ToolCall, result *tools.ToolResult) {
	if !a.verify {
		return
	}
	switch result.Name {
	case "write", "edit":
		if result.IsError {
			return
		}
		var args struct {
			Path string `json:"path"`
		}
		if json.Unmarshal([]byte(call.Arguments), &args) != nil || args.Path == "" {
			return
		}
		path := filepath.Clean(args.Path)
		if docExts[strings.ToLower(filepath.Ext(path))] || slices.Contains(a.unverified, path) {
			return
		}
		a.unverified = append(a.unverified, path)
	case "shell":
		// Only a foreground check that finished counts. Refused calls and
		// background jobs have no exit code here.
		var args tools.ShellArgs
		var res tools.ShellResponse
		if json.Unmarshal([]byte(call.Arguments), &args) != nil || (args.Action != "" && args.Action != "run") ||
			args.Background || json.Unmarshal([]byte(result.Content), &res) != nil || res.ExitCode == nil {
			return
		}
		output := ""
		if res.Output != nil {
			output = *res.Output
		}
		switch checkOutcome(args.CommandLine(), *res.ExitCode, output) {
		case checkPassed:
			a.unverified, a.verifyFailed = nil, false
		case checkFailed:
			a.verifyFailed = true
		}
	}
}

// verifyNudge asks the model, once, to check unverified changes before
// the turn ends. It reports whether the turn goes on.
func (a *Agent) verifyNudge() bool {
	if !a.verify || a.verifyNudged || len(a.unverified) == 0 || len(a.toolDefs) == 0 || a.halt.tripped() {
		return false
	}
	a.verifyNudged = true
	files := a.unverified
	more := ""
	if len(files) > verifyMaxFiles {
		more = " and " + strconv.Itoa(len(files)-verifyMaxFiles) + " more"
		files = files[:verifyMaxFiles]
	}
	id := "reminder.verify"
	if a.verifyFailed {
		id = "reminder.verify-failed"
	}
	content := custom.TextFill(id, map[string]string{"files": strings.Join(files, ", ") + more})
	a.messages = append(a.messages, inference.LlmMessage{Role: "user", Content: content})
	rec := transcript.UserMessage(content)
	rec.Display = verifyDisplay
	a.records = append(a.records, rec)
	return true
}
