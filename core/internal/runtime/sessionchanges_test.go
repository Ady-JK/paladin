package runtime

import (
	"errors"
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/agent"
)

func TestReadOnlyToolsDoNotClaimWorkspaceChanges(t *testing.T) {
	for _, test := range []struct {
		name, args string
		changes    bool
	}{
		{"read", `{}`, false},
		{"shell", `{"cmd":"ls"}`, false},
		{"shell", `{"cmd":"echo hi > file"}`, true},
		{"write", `{}`, true},
	} {
		if got := canChangeFiles(test.name, test.args); got != test.changes {
			t.Errorf("canChangeFiles(%s, %s) = %v", test.name, test.args, got)
		}
	}
}

// Files whose diff could not be built, or that did not fit, are named
// with the reason instead of silently left out of /review session.
func TestSessionChangesNameWhatTheyLeaveOut(t *testing.T) {
	files := []ChangedFile{{Path: "a.go"}, {Path: "broken.bin"}, {Path: "c.go"}}
	diff := func(path string) (string, error) {
		if path == "broken.bin" {
			return "", errors.New("snapshot object missing")
		}
		return "--- a/" + path + "\n+++ b/" + path + "\n+x\n", nil
	}
	got, err := renderSessionChanges(files, diff)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "+++ b/a.go") || !strings.Contains(got, "+++ b/c.go") {
		t.Fatalf("diffs missing: %s", got)
	}
	if !strings.Contains(got, "broken.bin: could not build its diff (snapshot object missing)") || strings.Contains(got, "every file") {
		t.Fatalf("the failed file must be named: %s", got)
	}

	// The size cap names every file it leaves out.
	got, err = renderSessionChanges(files[:1:1], diff)
	if err != nil || strings.Contains(got, "Not included") {
		t.Fatalf("everything fits: %v %s", err, got)
	}
	big := func(path string) (string, error) { return path + strings.Repeat("x", 60*1024) + "\n", nil }
	got, err = renderSessionChanges(files, big)
	if err != nil || !strings.Contains(got, "a.go") || !strings.Contains(got, "broken.bin: over the 100 KB limit") ||
		!strings.Contains(got, "c.go: over the 100 KB limit") {
		t.Fatalf("cut: %v %.300s", err, got[len(got)-300:])
	}

	// Nothing to show at all is an error, not an empty review.
	fail := func(string) (string, error) { return "", errors.New("gone") }
	if _, err := renderSessionChanges(files, fail); err == nil || !strings.Contains(err.Error(), "broken.bin") {
		t.Fatalf("all failed: %v", err)
	}
}

// The live change views reuse one folder snapshot until a file-changing tool
// settles, so the mark must move on exactly those tools.
func TestRunningChangeMarkCountsSettledFileChangingTools(t *testing.T) {
	h := &Hub{active: map[string]*hubTurn{}}
	if _, running := h.runningChangeMark("s"); running {
		t.Fatal("an idle session reported a running turn")
	}
	turn := &hubTurn{}
	h.active["s"] = turn
	tool := func(id, name, status string) TurnEvent {
		return TurnEvent{Event: "chat/tool", Tool: &agent.ToolActivity{ID: id, Name: name, Arguments: "{}", Status: status}}
	}
	turn.log = []TurnEvent{tool("1", "write", "running"), tool("2", "read", "ok")}
	if mark, running := h.runningChangeMark("s"); !running || mark != 0 {
		t.Fatalf("running or read-only tools moved the mark: %d %v", mark, running)
	}
	turn.log = []TurnEvent{tool("1", "write", "ok"), tool("2", "read", "ok"), tool("3", "edit", "ok")}
	if mark, _ := h.runningChangeMark("s"); mark != 2 {
		t.Fatalf("mark = %d, want 2", mark)
	}
}
