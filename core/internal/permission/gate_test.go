package permission

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Ady-JK/paladin/core/internal/tools"
)

func newTestGate() *Gate {
	return &Gate{Workspace: testWS, SessionID: "s1", Broker: NewBroker(), Home: testHome}
}

func shellArgs(cmd string) json.RawMessage {
	b, _ := json.Marshal(map[string]string{"reason": "t", "cmd": cmd})
	return b
}

// answer resolves the next card the gate raises.
func answer(t *testing.T, g *Gate, id, reply string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !g.Broker.Pending(g.SessionID, id) {
		if time.Now().After(deadline) {
			t.Fatal("no approval card appeared")
		}
		time.Sleep(2 * time.Millisecond)
	}
	if err := g.Broker.Resolve(g.SessionID, id, reply); err != nil {
		t.Fatal(err)
	}
}

func TestGateRunsOrdinaryCallsWithoutACard(t *testing.T) {
	g := newTestGate()
	for _, tc := range []struct{ name, args string }{
		{"shell", string(shellArgs("go test ./..."))},
		{"write", `{"path":"main.go","content":"x"}`},
		{"read", `{"path":"/etc/hosts"}`},
		{"web_fetch", `{"url":"http://localhost:3000"}`},
		{"browser", `{"action":"open","url":"https://example.com"}`},
	} {
		auth := g.Authorize(context.Background(), "c1", tc.name, json.RawMessage(tc.args), func(Approval) { t.Fatalf("%s asked", tc.name) })
		if !auth.Run {
			t.Errorf("%s did not run: %s", tc.name, auth.Content)
		}
	}
}

func TestGateAsksOnceForAFlaggedCommand(t *testing.T) {
	for _, tc := range []struct {
		reply string
		run   bool
	}{{ReplyOnce, true}, {ReplyDeny, false}} {
		g := newTestGate()
		var cards []Approval
		done := make(chan Auth, 1)
		go func() {
			done <- g.Authorize(context.Background(), "c1", "shell", shellArgs("rm -rf ~"), func(a Approval) { cards = append(cards, a) })
		}()
		answer(t, g, "c1", tc.reply)
		auth := <-done
		if auth.Run != tc.run {
			t.Fatalf("%s: run = %v", tc.reply, auth.Run)
		}
		if !tc.run && !strings.Contains(auth.Content, "denied by the user") {
			t.Fatalf("refusal copy: %s", auth.Content)
		}
		first := cards[0]
		if first.State != StatePending || !first.Destructive || first.Kind != KindShell || len(first.Reasons) == 0 ||
			strings.Join(first.Choices, ",") != "once,deny" {
			t.Fatalf("card %+v", first)
		}
	}
}

func TestGateAsksForCredentialFilesThroughFileTools(t *testing.T) {
	g := newTestGate()
	done := make(chan Auth, 1)
	go func() {
		done <- g.Authorize(context.Background(), "c1", "read", json.RawMessage(`{"path":"/Users/dev/.ssh/id_rsa"}`), func(Approval) {})
	}()
	answer(t, g, "c1", ReplyDeny)
	if (<-done).Run {
		t.Fatal("a denied credential read ran")
	}
}

func TestGateHooksCanSkipOrForceTheQuestion(t *testing.T) {
	g := newTestGate()
	allowed := g.Authorize(WithHookVerdict(context.Background(), HookAllow), "c1", "shell", shellArgs("rm -rf ~"),
		func(Approval) { t.Fatal("an allowing hook should skip the card") })
	if !allowed.Run {
		t.Fatal("hook allow did not run")
	}
	done := make(chan Auth, 1)
	go func() {
		done <- g.Authorize(WithHookVerdict(context.Background(), HookAsk), "c2", "shell", shellArgs("ls"), func(Approval) {})
	}()
	answer(t, g, "c2", ReplyOnce)
	if !(<-done).Run {
		t.Fatal("hook ask then once did not run")
	}
}

func TestGateInterruptedWaitDoesNotRun(t *testing.T) {
	g := newTestGate()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan Auth, 1)
	go func() { done <- g.Authorize(ctx, "c1", "shell", shellArgs("rm -rf ~"), func(Approval) {}) }()
	for !g.Broker.Pending("s1", "c1") {
		time.Sleep(2 * time.Millisecond)
	}
	cancel()
	if auth := <-done; auth.Run {
		t.Fatal("an interrupted wait ran the command")
	}
}

func TestGatePlanModeLooksButDoesNotTouch(t *testing.T) {
	g := newTestGate()
	g.Plan = true
	for _, tc := range []struct {
		name, args string
		run        bool
	}{
		{"write", `{"path":"a.go","content":"x"}`, false},
		{"edit", `{"path":"a.go"}`, false},
		{"browser", `{"action":"open"}`, false},
		{"shell", string(shellArgs("rm file")), false},
		{"shell", string(shellArgs("ls -la")), true},
		{"read", `{"path":"a.go"}`, true},
	} {
		auth := g.Authorize(context.Background(), "c1", tc.name, json.RawMessage(tc.args), func(Approval) { t.Fatalf("%s asked", tc.name) })
		if auth.Run != tc.run {
			t.Errorf("plan %s %s: run = %v (%s)", tc.name, tc.args, auth.Run, auth.Content)
		}
	}
}

func TestGatePlanModeAsksForEveryMCPCall(t *testing.T) {
	g := newTestGate()
	g.Plan = true
	g.MCP = func(name string) (tools.MCPInfo, bool) {
		return tools.MCPInfo{Server: "dummy", Tool: "echo", ReadOnly: true}, name == "mcp__dummy__echo"
	}
	var card Approval
	done := make(chan Auth, 1)
	go func() {
		done <- g.Authorize(context.Background(), "c1", "mcp__dummy__echo", json.RawMessage(`{}`), func(a Approval) {
			if a.State == StatePending {
				card = a
			}
		})
	}()
	answer(t, g, "c1", ReplyOnce)
	if !(<-done).Run || card.Server != "dummy" || card.MCPTool != "echo" || strings.Join(card.Choices, ",") != "once,deny" {
		t.Fatalf("card %+v", card)
	}
}

func TestMCPCallsRunFreelyOutsidePlanMode(t *testing.T) {
	g := newTestGate()
	g.MCP = func(name string) (tools.MCPInfo, bool) {
		return tools.MCPInfo{Server: "s", Tool: "t", Destructive: true}, true
	}
	auth := g.Authorize(context.Background(), "c1", "mcp__s__t", json.RawMessage(`{}`), func(Approval) { t.Fatal("asked") })
	if !auth.Run {
		t.Fatal("an MCP call outside plan mode should run")
	}
}

func TestParsePresetAcceptsEveryOlderValue(t *testing.T) {
	for _, s := range []string{"", "auto", "read-only", "full-access"} {
		if p, err := ParsePreset(s); err != nil || p != FullAccess {
			t.Errorf("%q -> %v, %v", s, p, err)
		}
	}
	if _, err := ParsePreset("yolo"); err == nil {
		t.Fatal("garbage accepted")
	}
}

func TestTrustProjectAlwaysTrusts(t *testing.T) {
	if !newTestGate().TrustProject(context.Background(), "c", "eslint", func(Approval) {}) {
		t.Fatal("project tools should run")
	}
}
