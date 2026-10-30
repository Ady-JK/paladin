package permission

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/tools"
)

func mcpReq(server, tool string, readOnly, destructive bool) Request {
	return ClassifyMCP("mcp__"+server+"__"+tool, tools.MCPInfo{Server: server, Tool: tool, ReadOnly: readOnly, Destructive: destructive}, json.RawMessage(`{"q":1}`))
}

func TestDecideMCP(t *testing.T) {
	plain := mcpReq("gh", "create_issue", false, false)
	if plain.Kind != KindMCP || plain.Server != "gh" || plain.MCPTool != "create_issue" || plain.Args != `{"q":1}` {
		t.Fatalf("%+v", plain)
	}
	d := Decide(Auto, plain, nil)
	if d.Verdict != Ask || len(d.Choices) != 4 || len(d.Remember) != 1 ||
		d.Remember[0] != (Rule{Tool: ToolMCP, Pattern: "gh/create_issue", Action: ActionAllow}) {
		t.Fatalf("auto asks and remembers the tool: %+v", d)
	}
	if d := Decide(Auto, mcpReq("gh", "search", true, false), nil); d.Verdict != Allow {
		t.Fatalf("read-only hint runs: %+v", d)
	}
	if d := Decide(ReadOnly, mcpReq("gh", "search", true, false), nil); d.Verdict != Allow {
		t.Fatalf("read-only hint runs in read-only mode: %+v", d)
	}
	if d := Decide(ReadOnly, plain, nil); d.Verdict != Ask {
		t.Fatalf("read-only preset asks: %+v", d)
	}
	if d := Decide(FullAccess, plain, nil); d.Verdict != Allow {
		t.Fatalf("full access: %+v", d)
	}
	destructive := mcpReq("gh", "delete_repo", false, true)
	d = Decide(Auto, destructive, []Rule{{Tool: ToolMCP, Pattern: "gh", Action: ActionAllow}})
	if d.Verdict != Ask || !d.Destructive || len(d.Choices) != 2 || len(d.Remember) != 0 {
		t.Fatalf("destructive always asks once/deny: %+v", d)
	}
	for _, pattern := range []string{"*", "gh", "gh/*", "gh/create_issue"} {
		rules := []Rule{{Tool: ToolMCP, Pattern: pattern, Action: ActionAllow}}
		if d := Decide(Auto, plain, rules); d.Verdict != Allow || d.Rule == nil {
			t.Errorf("allow %q: %+v", pattern, d)
		}
		rules[0].Action = ActionDeny
		if d := Decide(FullAccess, mcpReq("gh", "search", true, false), []Rule{{Tool: ToolMCP, Pattern: strings.Replace(pattern, "create_issue", "search", 1), Action: ActionDeny}}); d.Verdict != Deny {
			t.Errorf("deny %q beats everything: %+v", pattern, d)
		}
	}
	for _, pattern := range []string{"other", "gh/other", "g", "other/*"} {
		if d := Decide(Auto, plain, []Rule{{Tool: ToolMCP, Pattern: pattern, Action: ActionAllow}}); d.Verdict != Ask {
			t.Errorf("%q should not match: %+v", pattern, d)
		}
	}
	// Shell rules never cover MCP tools and vice versa.
	if d := Decide(Auto, plain, []Rule{{Tool: ToolShell, Pattern: "*", Action: ActionAllow}}); d.Verdict != Ask {
		t.Fatalf("shell rule leaked: %+v", d)
	}
	if d := Decide(Auto, shellReq("npm test"), []Rule{{Tool: ToolMCP, Pattern: "*", Action: ActionAllow}}); d.Verdict != Ask {
		t.Fatalf("mcp rule leaked: %+v", d)
	}
}

func TestMCPRuleValidation(t *testing.T) {
	for _, p := range []string{"*", "gh", "gh/*", "gh/create_issue", "my-server/do_thing"} {
		if err := (Rule{Tool: ToolMCP, Pattern: p, Action: ActionAllow}).validate(); err != nil {
			t.Errorf("%q: %v", p, err)
		}
	}
	for _, p := range []string{"", "a/b/c", "/x", "x/", "*/x"} {
		if err := (Rule{Tool: ToolMCP, Pattern: p, Action: ActionAllow}).validate(); err == nil {
			t.Errorf("%q should be rejected", p)
		}
	}
}

func TestGateMCP(t *testing.T) {
	g := newGate(t, Auto)
	g.MCP = func(name string) (tools.MCPInfo, bool) {
		if name == "mcp__gh__create_issue" {
			return tools.MCPInfo{Server: "gh", Tool: "create_issue"}, true
		}
		return tools.MCPInfo{}, false
	}
	rec := &recorder{}
	answer(t, g.Broker, "s1", "c1", ReplyFolder)
	auth := g.Authorize(context.Background(), "c1", "mcp__gh__create_issue", json.RawMessage(`{"title":"x"}`), rec.notify)
	if !auth.Run || auth.Sandbox != nil {
		t.Fatalf("%+v", auth)
	}
	card := rec.evs[0]
	if card.Kind != KindMCP || card.Server != "gh" || card.MCPTool != "create_issue" || card.Args != `{"title":"x"}` {
		t.Fatalf("card %+v", card)
	}
	rec2 := &recorder{}
	if auth := g.Authorize(context.Background(), "c2", "mcp__gh__create_issue", json.RawMessage(`{}`), rec2.notify); !auth.Run || rec2.count() != 0 {
		t.Fatalf("folder rule should cover the next call: %+v %d", auth, rec2.count())
	}
	answer(t, g.Broker, "s1", "c3", ReplyDeny)
	g2 := newGate(t, Auto)
	g2.MCP, g2.Broker = g.MCP, g.Broker
	if auth := g2.Authorize(context.Background(), "c3", "mcp__gh__create_issue", nil, func(Approval) {}); auth.Run || !strings.Contains(auth.Content, "MCP tool gh/create_issue") {
		t.Fatalf("deny %+v", auth)
	}
	// Unknown tools without an MCP lookup still pass through.
	if auth := g.Authorize(context.Background(), "c4", "read", json.RawMessage(`{}`), func(Approval) {}); !auth.Run {
		t.Fatal("read should not be gated")
	}
}
