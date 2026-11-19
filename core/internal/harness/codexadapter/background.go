package codexadapter

import (
	"encoding/json"

	"github.com/Ady-JK/paladin/core/internal/codex"
)

// A command Codex runs in a background terminal outlives the turn that started it:
// its item stays inProgress after turn/completed and completes minutes later, with
// the original turn's id, while no turn is running. These helpers keep that
// lifecycle straight, so a turn that ends with such a command still running does
// not call it failed, and the real outcome lands when Codex reports it.

// Settled reports a command item that has reached an outcome: completed, failed or
// declined, whatever turn it belongs to.
func Settled(item codex.Raw) bool {
	if item.Str("type") != "commandExecution" {
		return false
	}
	switch item.Str("status") {
	case "completed", "failed", "declined":
		return true
	}
	return false
}

// BackgroundArgs marks a shell card's arguments as background work, so the card
// reads "running in background" instead of a command that was cut off.
func BackgroundArgs(args string) string {
	var m map[string]any
	if json.Unmarshal([]byte(args), &m) != nil || m == nil {
		m = map[string]any{}
	}
	m["background"] = true
	return jsonOf(m)
}

// RunningResult is the result of a command that is still running: what it has printed so far.
func RunningResult(output string) string {
	return jsonOf(map[string]any{"status": "running", "output": output})
}

// Unresolved reports a stored shell result that never got its outcome: a command left
// running in the background, or one an older Paladin closed as unknown or interrupted
// when its turn ended. Such a row is looked up again in Codex's own record.
func Unresolved(result string) bool {
	var res map[string]any
	if json.Unmarshal([]byte(result), &res) != nil {
		return false
	}
	// Codex's own record is the arbiter of an exit code the card already has.
	if _, exited := res["exit_code"]; exited {
		return false
	}
	status, _ := res["status"].(string)
	msg, _ := res["error"].(string)
	return status == "running" || status == "unknown" || msg == "interrupted before it finished"
}

// UnknownResult is the result of a command Paladin can no longer follow, keeping what
// it had printed: its outcome is not known, which is not the same as failed.
func UnknownResult(stored string) string {
	var res struct {
		Output string `json:"output"`
	}
	_ = json.Unmarshal([]byte(stored), &res)
	return jsonOf(map[string]any{"status": "unknown", "output": res.Output})
}
