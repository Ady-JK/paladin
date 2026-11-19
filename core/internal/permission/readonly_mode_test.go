package permission

import (
	"context"
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/tools"
)

func TestReadOnlyBlocksChangingToolsDespiteGrantsAndHooks(t *testing.T) {
	g := newGate(t, ReadOnly)
	g.MCP = func(name string) (tools.MCPInfo, bool) {
		return tools.MCPInfo{Server: "test", Tool: "change", ReadOnly: true}, name == "mcp__test__change"
	}
	for _, tool := range []string{ToolWrite, ToolShell, ToolMCP} {
		if err := g.Store.Add(ScopeGlobal, "", "", Rule{Tool: tool, Pattern: "*", Action: ActionAllow}); err != nil {
			t.Fatal(err)
		}
	}
	for _, call := range []struct{ tool, input string }{
		{"write", `{"path":"/ws/test.txt","content":"changed"}`},
		{"edit", `{"path":"/ws/test.txt","old_string":"x","new_string":"y"}`},
		{"shell", `{"cmd":"npm test"}`},
		{"shell", `{"cmd":"echo changed > /ws/test.txt"}`},
		{"browser", `{"action":"click","selector":"button"}`},
		{"visual", `{"action":"generate","prompt":"test"}`},
		{"mcp__test__change", `{}`},
	} {
		rec := &recorder{}
		auth := g.Authorize(WithHookVerdict(context.Background(), HookAllow), "call", call.tool, []byte(call.input), rec.notify)
		if auth.Run || !strings.Contains(auth.Content, "read-only") || rec.count() != 0 {
			t.Errorf("%s %s: %+v, approvals %d", call.tool, call.input, auth, rec.count())
		}
	}
}

func TestReadOnlyDoesNotTrustProjectCode(t *testing.T) {
	g := newGate(t, ReadOnly)
	if g.TrustProject(context.Background(), "call", "node_modules/.bin/prettier", (&recorder{}).notify) {
		t.Fatal("read-only mode trusted arbitrary project code")
	}
}
