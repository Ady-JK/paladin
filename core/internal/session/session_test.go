package session

import (
	"testing"
	"time"

	"github.com/Ady-JK/paladin/core/internal/transcript"
)

func TestHistoryLinksToolResultsToCalls(t *testing.T) {
	calls := []transcript.ToolCall{{ID: "c1", Name: "read", Arguments: `{"path":"workspace/a"}`}}
	msgs := []transcript.Message{
		{Role: transcript.RoleAssistant, Content: "", CreatedAt: time.Now().UTC(), ToolCalls: &calls},
		{Role: transcript.RoleTool, Content: "content", CreatedAt: time.Now().UTC(), ToolCallID: strp("c1"), Name: strp("read")},
	}
	view := HistoryView(msgs)
	if len(view) != 2 {
		t.Fatalf("got %d", len(view))
	}
	if view[1].Arguments == nil || *view[1].Arguments != `{"path":"workspace/a"}` {
		t.Fatalf("got %+v", view[1])
	}
	if view[1].Status == nil || *view[1].Status != "ok" {
		t.Fatalf("got %+v", view[1])
	}
}

func strp(s string) *string { return &s }

// ---- merged from session_extra_test.go ----

func TestNotifyHelpers(t *testing.T) {
	if got := NotifyCallID("c1"); got != "c1#notify" {
		t.Fatalf("got %q", got)
	}
	args := NotifyArgs("job-1")
	if args == "" || !contains(args, "job-1") {
		t.Fatalf("got %q", args)
	}
	callID := "c9"
	name := "shell"
	notifyMsg := transcript.Message{Role: transcript.RoleTool, Content: args, CreatedAt: time.Now().UTC(), ToolCallID: &callID, Name: &name, IsNotify: true}
	if !IsNotifyMessage(notifyMsg) {
		t.Fatal("IsNotify flag missed")
	}
	suffixed := transcript.Message{Role: transcript.RoleTool, ToolCallID: strp("c1#notify")}
	if !IsNotifyMessage(suffixed) {
		t.Fatal("suffix missed")
	}
	plain := transcript.Message{Role: transcript.RoleTool, ToolCallID: strp("c1")}
	if IsNotifyMessage(plain) {
		t.Fatal("false positive")
	}
}

func TestInfoFromShapes(t *testing.T) {
	prov, model := "p", "m"
	info := InfoFrom(transcript.Meta{SessionID: "s", Cwd: "/w", CreatedAt: time.Unix(10, 0).UTC(), Provider: &prov, Model: &model}, 3, "title")
	if info.SessionID != "s" || info.Cwd != "/w" || info.CreatedAt != 10 || info.Provider != "p" || info.Model != "m" || info.MessageCount != 3 || info.Title != "title" {
		t.Fatalf("got %+v", info)
	}
	bare := InfoFrom(transcript.Meta{SessionID: "s"}, 0, "")
	if bare.Provider != "" || bare.Model != "" {
		t.Fatalf("got %+v", bare)
	}
}

func TestHistoryViewMatrix(t *testing.T) {
	now := time.Now().UTC()
	callID, name := "c1", "read"
	otherID, otherName := "missing", "shell"
	msgs := []transcript.Message{
		transcript.UserMessage("hi"),
		{Role: transcript.RoleAssistant, Content: "", CreatedAt: now},
		{Role: transcript.RoleAssistant, Content: "ans", CreatedAt: now},
		{Role: transcript.RoleSystem, Content: "sys", CreatedAt: now},
		{Role: transcript.RoleAssistant, Content: "", CreatedAt: now, ToolCalls: &[]transcript.ToolCall{{ID: "c1", Name: "read", Arguments: `{"path":"x"}`}}},
		{Role: transcript.RoleTool, Content: "out", CreatedAt: now, ToolCallID: &callID, Name: &name},
		{Role: transcript.RoleTool, Content: "orphan", CreatedAt: now, ToolCallID: &otherID, Name: &otherName, IsError: true},
	}
	view := HistoryView(msgs)
	// user, blank-assistant dropped, assistant, system, assistant-with-calls dropped (blank, no content? kept? it has calls -> visible? HistoryView keeps assistant rows only via Visible: blank with calls IS visible), 2 tool rows.
	roles := map[string]int{}
	for _, h := range view {
		roles[h.Role]++
	}
	if roles["user"] != 1 || roles["assistant"] != 2 || roles["system"] != 1 || roles["tool"] != 2 {
		t.Fatalf("roles %v in %+v", roles, view)
	}
	var linked, orphan *HistoryMessage
	for i := range view {
		if view[i].Role != "tool" {
			continue
		}
		if view[i].ToolCallID != nil && *view[i].ToolCallID == "c1" {
			linked = &view[i]
		} else {
			orphan = &view[i]
		}
	}
	if linked == nil || orphan == nil {
		t.Fatalf("missing rows: %+v", view)
	}
	if linked.Arguments == nil || *linked.Arguments != `{"path":"x"}` {
		t.Fatalf("linked args: %+v", linked)
	}
	if linked.Status == nil || *linked.Status != ToolOK {
		t.Fatalf("linked status: %+v", linked)
	}
	if orphan.Arguments != nil {
		t.Fatalf("orphan should have no args: %+v", orphan)
	}
	if orphan.Status == nil || *orphan.Status != ToolFailed {
		t.Fatalf("orphan status: %+v", orphan)
	}
	// Notify rows recover their job link for arguments.
	notifyContent := `{"notice":"background job finished","job_id":"job-7","status":"done","output":"hi"}`
	nid := "c1#notify"
	nname := "shell"
	nview := HistoryView([]transcript.Message{
		{Role: transcript.RoleTool, Content: notifyContent, CreatedAt: now, ToolCallID: &nid, Name: &nname},
	})
	if len(nview) != 1 || nview[0].Arguments == nil || !contains(*nview[0].Arguments, "job-7") {
		t.Fatalf("notify args: %+v", nview)
	}
	// Empty history -> empty (non-nil) view.
	if out := HistoryView(nil); out == nil || len(out) != 0 {
		t.Fatalf("got %+v", out)
	}
	// Duplicate call IDs keep the first registration.
	dup := []transcript.Message{
		{Role: transcript.RoleAssistant, Content: "a", CreatedAt: now, ToolCalls: &[]transcript.ToolCall{{ID: "d", Name: "read", Arguments: `{"path":"first"}`}}},
		{Role: transcript.RoleAssistant, Content: "b", CreatedAt: now, ToolCalls: &[]transcript.ToolCall{{ID: "d", Name: "read", Arguments: `{"path":"second"}`}}},
		{Role: transcript.RoleTool, Content: "out", CreatedAt: now, ToolCallID: strp("d"), Name: strp("read")},
	}
	dview := HistoryView(dup)
	if len(dview) != 3 || dview[2].Arguments == nil || *dview[2].Arguments != `{"path":"first"}` {
		t.Fatalf("dup ids: %+v", dview)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
