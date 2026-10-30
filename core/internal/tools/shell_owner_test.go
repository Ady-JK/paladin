package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func ownerCtx(t *testing.T, sessionID string) *ToolCtx {
	t.Helper()
	ws := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	return &ToolCtx{Workspace: ws, SessionDir: t.TempDir(), Cancel: context.Background(), SessionID: sessionID}
}

// The shell tool is shared by every session, so a job id must only
// work for the session that started the job.
func TestJobsAnswerOnlyTheirOwnSession(t *testing.T) {
	tool := NewShellTool()
	owner := ownerCtx(t, "sess-owner")
	id := startJob(t, tool, owner, `{"cmd":"sleep 30","background":true}`)
	t.Cleanup(func() { _, _ = tool.Call(json.RawMessage(`{"action":"cancel","job_id":"`+id+`"}`), owner) })

	other := ownerCtx(t, "sess-other")
	for _, action := range []string{"status", "cancel"} {
		_, err := tool.Call(json.RawMessage(`{"action":"`+action+`","job_id":"`+id+`"}`), other)
		if err == nil || !strings.Contains(err.Error(), "unknown job") {
			t.Fatalf("%s from another session: err = %v", action, err)
		}
	}
	// The plan-mode (read-only child) wrapper is held to the same rule.
	if _, err := NewPlanShell(tool).Call(json.RawMessage(`{"action":"cancel","job_id":"`+id+`"}`), other); err == nil {
		t.Fatal("plan shell cancelled another session's job")
	}

	out, err := tool.Call(json.RawMessage(`{"action":"status","job_id":"`+id+`"}`), owner)
	if err != nil || !strings.Contains(out, JobRunning) {
		t.Fatalf("owner status = %s, %v (the job must still run)", out, err)
	}
	if out, err = tool.Call(json.RawMessage(`{"action":"cancel","job_id":"`+id+`"}`), owner); err != nil || !strings.Contains(out, JobCancelled) {
		t.Fatalf("owner cancel = %s, %v", out, err)
	}
}
