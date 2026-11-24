package permission

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/Ady-JK/paladin/core/internal/tools"
)

// Approval states.
const (
	StatePending  = "pending"
	StateApproved = "approved"
	StateDenied   = "denied"
)

// Approval is the chat/approval event payload: one card in the UI.
type Approval struct {
	ID      string `json:"id"`
	Tool    string `json:"tool"`
	Kind    string `json:"kind"`
	Command string `json:"command,omitempty"`
	Path    string `json:"path,omitempty"`
	// Server, MCPTool and Args describe an MCP call.
	Server      string   `json:"server,omitempty"`
	MCPTool     string   `json:"mcpTool,omitempty"`
	Args        string   `json:"args,omitempty"`
	Reasons     []string `json:"reasons"`
	Destructive bool     `json:"destructive,omitempty"`
	Choices     []string `json:"choices"`
	State       string   `json:"state"`
	Decision    string   `json:"decision,omitempty"`
}

// Broker parks approval waits until the UI answers.
type Broker struct {
	mu      sync.Mutex
	pending map[string]chan string
}

// NewBroker builds an empty broker.
func NewBroker() *Broker { return &Broker{pending: map[string]chan string{}} }

func brokerKey(sessionID, id string) string { return sessionID + "\x00" + id }

// Open registers a wait before the card is shown, so an instant answer
// cannot race ahead of it.
func (b *Broker) Open(sessionID, id string) chan string {
	ch := make(chan string, 1)
	b.mu.Lock()
	b.pending[brokerKey(sessionID, id)] = ch
	b.mu.Unlock()
	return ch
}

// Wait blocks for the answer or the turn's end.
func (b *Broker) Wait(ctx context.Context, sessionID, id string, ch chan string) (string, error) {
	defer func() {
		b.mu.Lock()
		if b.pending[brokerKey(sessionID, id)] == ch {
			delete(b.pending, brokerKey(sessionID, id))
		}
		b.mu.Unlock()
	}()
	select {
	case r := <-ch:
		return r, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// Resolve answers a pending approval.
func (b *Broker) Resolve(sessionID, id, reply string) error {
	switch reply {
	case ReplyOnce, ReplySession, ReplyFolder, ReplyGlobal, ReplyDeny:
	default:
		return fmt.Errorf("decision must be once, session, folder, global or deny, got %q", reply)
	}
	key := brokerKey(sessionID, id)
	b.mu.Lock()
	ch, ok := b.pending[key]
	if ok {
		delete(b.pending, key)
	}
	b.mu.Unlock()
	if !ok {
		return fmt.Errorf("no pending approval %q (the turn may have moved on)", id)
	}
	ch <- reply
	return nil
}

// Pending reports whether an approval is waiting.
func (b *Broker) Pending(sessionID, id string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.pending[brokerKey(sessionID, id)]
	return ok
}

// Gate is one turn's checkpoint: every call runs unless the guard flags it.
type Gate struct {
	Workspace string
	SessionID string
	Broker    *Broker
	// MCP identifies tools proxied from MCP servers; nil means none.
	MCP func(name string) (tools.MCPInfo, bool)
	// Plan is plan mode: nothing may change anything, and every MCP call
	// asks (a server cannot prove a tool is read-only).
	Plan bool
	// Home is the home folder the guard protects; empty uses the user's.
	Home string
}

// Auth is the gate's answer for one call.
type Auth struct {
	Run bool
	// Content is the tool result to record when Run is false.
	Content string
}

func refusal(msg string) string {
	b, _ := json.Marshal(map[string]string{"error": msg})
	return string(b)
}

func (g *Gate) home() string {
	if g.Home != "" {
		return g.Home
	}
	h, _ := os.UserHomeDir()
	return h
}

// review is what a call is and why, if at all, it needs the user's say.
type review struct {
	kind, command, path, server, mcpTool, args string
	reasons                                    []string
	destructive                                bool
}

func (g *Gate) review(name string, raw json.RawMessage) (review, bool) {
	switch name {
	case "shell":
		var a tools.ShellArgs
		if json.Unmarshal(raw, &a) != nil || (a.Action != "" && a.Action != "run") {
			return review{}, false
		}
		cmd := a.CommandLine()
		if strings.TrimSpace(cmd) == "" {
			return review{}, false
		}
		return review{kind: KindShell, command: cmd, reasons: CheckShell(cmd, g.Workspace, g.home())}, true
	case "write", "edit", "read", "grep", "glob", "list":
		var a struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(raw, &a) != nil || strings.TrimSpace(a.Path) == "" {
			return review{}, false
		}
		path, _, err := tools.ResolveScopedPath(name, "path", a.Path, g.Workspace)
		if err != nil {
			return review{}, false // the tool rejects it itself
		}
		write := name == "write" || name == "edit"
		kind := KindRead
		if write {
			kind = KindWrite
		}
		reasons := CheckPath(path, write, g.home())
		if real := tools.RealPath(path); real != path {
			for _, r := range CheckPath(real, write, g.home()) {
				reasons = appendUnique(reasons, r)
			}
		}
		return review{kind: kind, path: path, reasons: reasons}, true
	}
	if g.MCP != nil {
		if info, ok := g.MCP(name); ok {
			args := strings.TrimSpace(string(raw))
			if len(args) > 4000 {
				args = args[:4000] + "…"
			}
			return review{kind: KindMCP, server: info.Server, mcpTool: info.Tool, args: args, destructive: info.Destructive}, true
		}
	}
	return review{}, false
}

func appendUnique(list []string, s string) []string {
	for _, e := range list {
		if e == s {
			return list
		}
	}
	return append(list, s)
}

// Authorize decides one call, asking the user (and blocking) only when the
// guard flags it, a hook asks, or plan mode needs an MCP call confirmed.
func (g *Gate) Authorize(ctx context.Context, callID, name string, args json.RawMessage, notify func(Approval)) Auth {
	if g.Plan {
		if refused, why := g.planRefusal(name, args); refused {
			return Auth{Content: refusal(why)}
		}
	}
	rv, gated := g.review(name, args)
	if !gated {
		return Auth{Run: true}
	}
	hook, _ := ctx.Value(hookVerdictKey{}).(string)
	switch {
	case g.Plan && rv.kind == KindMCP:
		rv.reasons = []string{"plan mode: the server's read-only claim cannot be verified"}
	case hook == HookAsk && len(rv.reasons) == 0:
		rv.reasons = []string{"a hook asked for confirmation"}
	case hook == HookAllow && !(g.Plan && rv.kind == KindMCP):
		rv.reasons = nil
	}
	if len(rv.reasons) == 0 {
		return Auth{Run: true}
	}
	card := Approval{ID: callID, Tool: name, Kind: rv.kind, Command: rv.command, Path: rv.path,
		Server: rv.server, MCPTool: rv.mcpTool, Args: rv.args, Reasons: rv.reasons,
		Destructive: rv.destructive || rv.kind != KindMCP, Choices: []string{ReplyOnce, ReplyDeny}, State: StatePending}
	reply, err := g.ask(ctx, card, notify)
	if err != nil {
		return Auth{Content: refusal("interrupted while waiting for the user's approval; it did not run")}
	}
	if reply == ReplyDeny {
		return Auth{Content: refusal("denied by the user: they declined this " + describe(rv) + ". Do not retry it; continue another way or ask the user what they want.")}
	}
	return Auth{Run: true}
}

// planRefusal is plan mode's rule: look, don't touch.
func (g *Gate) planRefusal(name string, raw json.RawMessage) (bool, string) {
	switch name {
	case "write", "edit", "browser", "visual":
		return true, "plan mode: " + name + " is not available; describe the change in the plan instead"
	case "shell":
		var a tools.ShellArgs
		if json.Unmarshal(raw, &a) == nil && (a.Action == "" || a.Action == "run") && tools.ReadOnlyCommand(a.CommandLine()) != nil {
			return true, "plan mode: only inspection commands run; describe the change in the plan instead"
		}
	}
	return false, ""
}

// TrustProject: tools a workspace ships (formatters, language servers) run
// without a question.
func (g *Gate) TrustProject(context.Context, string, string, func(Approval)) bool { return true }

func (g *Gate) ask(ctx context.Context, card Approval, notify func(Approval)) (string, error) {
	if g.Broker == nil {
		return "", errors.New("no approval broker")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ch := g.Broker.Open(g.SessionID, card.ID)
	notify(card)
	reply, err := g.Broker.Wait(ctx, g.SessionID, card.ID, ch)
	if err != nil {
		card.State, card.Decision = StateDenied, "interrupted"
		notify(card)
		return "", err
	}
	card.Decision = reply
	card.State = StateApproved
	if reply == ReplyDeny {
		card.State = StateDenied
	}
	notify(card)
	return reply, nil
}

func describe(rv review) string {
	switch rv.kind {
	case KindWrite:
		return "write to " + rv.path
	case KindRead:
		return "read of " + rv.path
	case KindMCP:
		return "MCP tool " + rv.server + "/" + rv.mcpTool
	}
	return "command `" + strings.TrimSpace(rv.command) + "`"
}

// Hook verdicts a PreToolUse hook may hand the gate.
const (
	HookAllow = "allow"
	HookAsk   = "ask"
)

type hookVerdictKey struct{}

// WithHookVerdict carries a PreToolUse hook's "allow" or "ask" into
// Authorize. Deny is handled by the caller (the call never gets here).
func WithHookVerdict(ctx context.Context, verdict string) context.Context {
	if verdict != HookAllow && verdict != HookAsk {
		return ctx
	}
	return context.WithValue(ctx, hookVerdictKey{}, verdict)
}
