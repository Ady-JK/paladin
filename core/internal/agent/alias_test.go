package agent

import (
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/inference"
	"github.com/Ady-JK/paladin/core/internal/session"
	"github.com/Ady-JK/paladin/core/internal/tools"
)

// TestBashAliasRunsShellAndIsRecordedAsShell is the end-to-end case:
// the model asks for a tool this harness does not advertise ("bash"),
// the shell tool runs, and the conversation saved — and therefore
// replayed on later rounds — says "shell".
func TestBashAliasRunsShellAndIsRecordedAsShell(t *testing.T) {
	dir := t.TempDir()
	prov := &mockProvider{streams: []streamScript{
		{chunks: []inference.Chunk{{ToolCalls: []inference.ToolCall{
			{ID: "c1", Name: "bash", Arguments: `{"command":"echo aliased"}`},
		}}}},
		{chunks: []inference.Chunk{inference.TextChunk("done")}},
	}}
	a, sess, tx, done := testAgent(t, prov)
	defer close(done)
	a.toolCtx.Workspace = dir

	outcome, err := a.Run()
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Reply != "done" {
		t.Fatalf("got %+v", outcome)
	}

	// The command really ran.
	var toolChunks []*ToolActivity
	for _, c := range drainChunks(tx) {
		if c.Tool != nil {
			toolChunks = append(toolChunks, c.Tool)
		}
	}
	if len(toolChunks) < 2 {
		t.Fatalf("want running + done tool events, got %+v", toolChunks)
	}
	for _, c := range toolChunks {
		if c.Name != "shell" {
			t.Fatalf("tool event name = %q, want shell", c.Name)
		}
	}
	if toolChunks[len(toolChunks)-1].Status != session.ToolOK {
		t.Fatalf("tool call failed: %+v", toolChunks[len(toolChunks)-1])
	}
	if !strings.Contains(toolChunks[len(toolChunks)-1].Result, "aliased") {
		t.Fatalf("command output missing: %+v", toolChunks[len(toolChunks)-1])
	}

	// The stored conversation records the canonical name in both the
	// assistant call and its result, so a later round never sees "bash".
	history, err := sess.History()
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 4 {
		t.Fatalf("got %d messages: %+v", len(history), history)
	}
	asst := history[1]
	if asst.ToolCalls == nil || len(*asst.ToolCalls) != 1 {
		t.Fatalf("assistant message has no single tool call: %+v", asst)
	}
	if got := (*asst.ToolCalls)[0].Name; got != "shell" {
		t.Fatalf("stored call name = %q, want shell", got)
	}
	res := history[2]
	if res.Name == nil || *res.Name != "shell" {
		t.Fatalf("stored result name = %v, want shell", res.Name)
	}
}

// TestBashAliasReplaysAsShellToTheModel is the "same for future
// rounds" half: on the round after the aliased call, the messages sent
// to the provider name shell, not bash.
func TestBashAliasReplaysAsShellToTheModel(t *testing.T) {
	dir := t.TempDir()
	prov := &mockProvider{streams: []streamScript{
		{chunks: []inference.Chunk{{ToolCalls: []inference.ToolCall{
			{ID: "c1", Name: "Bash", Arguments: `{"command":"echo hi"}`},
		}}}},
		{chunks: []inference.Chunk{inference.TextChunk("done")}},
	}}
	a, _, _, done := testAgent(t, prov)
	defer close(done)
	a.toolCtx.Workspace = dir
	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	if len(prov.streamMsgs) < 2 {
		t.Fatalf("expected two rounds, got %d", len(prov.streamMsgs))
	}
	// The second round's request carries the first round's call.
	second := prov.streamMsgs[1]
	var sawToolCall, sawToolResult bool
	for _, m := range second {
		for _, c := range m.ToolCalls {
			if c.ID == "c1" {
				sawToolCall = true
				if c.Name != "shell" {
					t.Fatalf("replayed call name = %q, want shell", c.Name)
				}
			}
		}
		if m.Role == "tool" && m.ToolCallID != nil && *m.ToolCallID == "c1" {
			sawToolResult = true
			if m.Name == nil || *m.Name != "shell" {
				t.Fatalf("replayed tool result name = %v, want shell", m.Name)
			}
		}
		if m.Role == "assistant" {
			for _, c := range m.ToolCalls {
				if strings.EqualFold(c.Name, "bash") {
					t.Fatalf("the model is shown the alias it used: %+v", c)
				}
			}
		}
	}
	if !sawToolCall || !sawToolResult {
		t.Fatalf("round two is missing the paired call/result (call=%v result=%v)", sawToolCall, sawToolResult)
	}
}

// TestBashAliasIsNotMentionedToTheModel: the mapping is internal. No
// tool definition and no tool result may name it, or the model learns
// to keep sending "bash" and the disguise is pointless.
func TestBashAliasIsNotMentionedToTheModel(t *testing.T) {
	reg := tools.BuiltinRegistry()
	for _, def := range reg.Definitions() {
		text := strings.ToLower(def.Name + " " + def.Description)
		// "bash" may legitimately appear as a shell name in prose
		// (shell_check / "not a bash shell"); what must not appear is
		// an alias instruction.
		if strings.Contains(text, "alias") {
			t.Fatalf("tool definition %q mentions aliases: %s", def.Name, def.Description)
		}
	}

	dir := t.TempDir()
	prov := &mockProvider{streams: []streamScript{
		{chunks: []inference.Chunk{{ToolCalls: []inference.ToolCall{
			{ID: "c1", Name: "bash", Arguments: `{"command":"echo quiet"}`},
		}}}},
		{chunks: []inference.Chunk{inference.TextChunk("done")}},
	}}
	a, _, tx, done := testAgent(t, prov)
	defer close(done)
	a.toolCtx.Workspace = dir
	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	for _, c := range drainChunks(tx) {
		if c.Tool == nil {
			continue
		}
		lower := strings.ToLower(c.Tool.Result)
		for _, tell := range []string{"there is no", "ran as", "call shell directly", "alias"} {
			if strings.Contains(lower, tell) {
				t.Fatalf("tool result leaks the mapping (%q): %s", tell, c.Tool.Result)
			}
		}
	}
}

// TestBashAliasRespectsMissingShellTool: when the registry has no
// shell (a read-only subset), the alias must not conjure one.
func TestBashAliasRespectsMissingShellTool(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(tools.ReadTool{})
	calls := []inference.ToolCall{{ID: "c1", Name: "bash", Arguments: `{"command":"echo hi"}`}}
	canonicalizeCalls(reg, calls)
	if calls[0].Name != "bash" {
		t.Fatalf("name = %q, want bash left alone so the call fails visibly", calls[0].Name)
	}
	res := reg.Call(&tools.ToolCall{ID: calls[0].ID, Name: calls[0].Name, Arguments: calls[0].Arguments}, &tools.ToolCtx{Workspace: t.TempDir()})
	if !res.IsError {
		t.Fatal("expected an unknown-tool error")
	}
	if !strings.Contains(res.Content, "unknown tool") {
		t.Fatalf("got %s", res.Content)
	}
}

// TestCanonicalizeCallsLeavesOtherNames alone guards the blast radius.
func TestCanonicalizeCallsLeavesOtherNames(t *testing.T) {
	reg := tools.BuiltinRegistry()
	calls := []inference.ToolCall{
		{ID: "a", Name: "read"},
		{ID: "b", Name: "Bash"},
		{ID: "c", Name: "patch"},
		{ID: "d", Name: "grep"},
	}
	canonicalizeCalls(reg, calls)
	want := []string{"read", "shell", "patch", "grep"}
	for i, w := range want {
		if calls[i].Name != w {
			t.Fatalf("call %d = %q, want %q", i, calls[i].Name, w)
		}
		if calls[i].ID != []string{"a", "b", "c", "d"}[i] {
			t.Fatalf("call %d id changed: %q", i, calls[i].ID)
		}
	}
}

// TestBashAliasInPlanModeStaysReadOnly: renaming a call to "shell" must
// hand it to whatever shell this registry holds — in plan mode that is
// the read-only wrapper, not the real tool. An alias must never become
// a way around the plan-mode fence.
func TestBashAliasInPlanModeStaysReadOnly(t *testing.T) {
	base := tools.BuiltinRegistry()
	realShell := tools.NewShellTool()
	base.Register(realShell)
	plan := tools.NewRegistry()
	for _, name := range base.Names() {
		if name == "write" || name == "edit" {
			continue
		}
		tool, _ := base.Get(name)
		if name == "shell" {
			plan.Register(tools.NewPlanShell(realShell))
			continue
		}
		plan.Register(tool)
	}

	calls := []inference.ToolCall{{ID: "c1", Name: "bash", Arguments: `{"command":"rm -rf build"}`}}
	canonicalizeCalls(plan, calls)
	if calls[0].Name != "shell" {
		t.Fatalf("name = %q, want shell", calls[0].Name)
	}
	res := plan.Call(&tools.ToolCall{ID: calls[0].ID, Name: calls[0].Name, Arguments: calls[0].Arguments},
		&tools.ToolCtx{Workspace: t.TempDir()})
	if !res.IsError {
		t.Fatalf("plan mode ran a destructive bash command: %+v", res)
	}
	if !strings.Contains(res.Content, "plan mode") {
		t.Fatalf("refusal did not come from plan mode: %s", res.Content)
	}
}
