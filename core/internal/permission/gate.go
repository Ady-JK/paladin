package permission

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Ady-JK/paladin/core/internal/sandbox"
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
	// Remember is what "this session"/"this folder" would save.
	Remember []Rule `json:"remember,omitempty"`
	State    string `json:"state"`
	Decision string `json:"decision,omitempty"`
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

// Gate is one turn's permission checkpoint.
type Gate struct {
	Preset    Preset
	Workspace string
	SessionID string
	Store     *Store
	Broker    *Broker
	// SandboxKind is sandbox.Kind(); "" means approvals only.
	SandboxKind string
	// ExtraWritable are more folders sandboxed shell calls may write,
	// e.g. the shared git dir of a worktree session.
	ExtraWritable []string
	// MCP identifies tools proxied from MCP servers; nil means none.
	MCP func(name string) (tools.MCPInfo, bool)
}

// Auth is the gate's answer for one call.
type Auth struct {
	Run bool
	// Content is the tool result to record when Run is false.
	Content string
	// Sandbox confines a shell call; nil runs it unconfined.
	Sandbox *sandbox.Policy
}

func refusal(msg string) string {
	b, _ := json.Marshal(map[string]string{"error": msg})
	return string(b)
}

// Authorize decides one call, asking the user (and blocking) if needed.
func (g *Gate) Authorize(ctx context.Context, callID, name string, args json.RawMessage, notify func(Approval)) Auth {
	req, gated := Classify(name, args, g.Workspace)
	if !gated && g.MCP != nil {
		if info, ok := g.MCP(name); ok {
			req, gated = ClassifyMCP(name, info, args), true
		}
	}
	if !gated {
		return Auth{Run: true}
	}
	var rules []Rule
	if g.Store != nil {
		rules = g.Store.Effective(g.Workspace, g.SessionID)
	}
	d := Decide(g.Preset, req, rules)
	d = applyHookVerdict(ctx, d)
	switch d.Verdict {
	case Deny:
		what := "this call"
		if d.Rule != nil {
			what = fmt.Sprintf("%s rule %q", d.Rule.Tool, d.Rule.Pattern)
		}
		return Auth{Content: refusal("blocked by a permission rule (" + what + "). Do not retry it; choose another approach or ask the user.")}
	case Allow:
		return Auth{Run: true, Sandbox: g.policy(req, d.Sandbox)}
	}
	card := Approval{ID: callID, Tool: name, Kind: req.Kind, Command: req.Command, Path: req.Path,
		Server: req.Server, MCPTool: req.MCPTool, Args: req.Args,
		Reasons: d.Reasons, Destructive: d.Destructive, Choices: d.Choices, Remember: d.Remember, State: StatePending}
	reply, err := g.ask(ctx, card, notify)
	if err != nil {
		return Auth{Content: refusal("interrupted while waiting for the user's approval; it did not run")}
	}
	if reply == ReplyDeny {
		return Auth{Content: refusal("denied by the user: they declined this " + describe(req) + ". Do not retry it; continue another way or ask the user what they want.")}
	}
	g.remember(reply, d.Remember)
	return Auth{Run: true, Sandbox: g.policy(req, d.Sandbox)}
}

// remember saves the rules a session/folder/global reply stands for.
func (g *Gate) remember(reply string, rules []Rule) {
	scope := map[string]string{ReplySession: ScopeSession, ReplyFolder: ScopeFolder, ReplyGlobal: ScopeGlobal}[reply]
	if scope == "" || g.Store == nil || len(rules) == 0 {
		return
	}
	_ = g.Store.Add(scope, g.Workspace, g.SessionID, rules...)
}

// Blocked is a shell call the sandbox stopped: the policy it ran under
// and what it printed.
type Blocked struct {
	Policy *sandbox.Policy
	Output string
}

// Escalation is the user's answer to a sandbox block. Run with a nil
// Sandbox reruns unconfined; with a policy it reruns confined to it.
type Escalation struct {
	Run     bool
	Sandbox *sandbox.Policy
}

// Escalate asks what to do about a shell call the sandbox blocked.
// When the output names the denied path, the card offers to grant its
// folder (as a write rule, so it also covers the write/edit tools) and
// rerun still sandboxed; "once" reruns without the sandbox.
func (g *Gate) Escalate(ctx context.Context, callID, name string, args json.RawMessage, b Blocked, notify func(Approval)) Escalation {
	if g.Preset == FullAccess || g.SandboxKind == "" || b.Policy == nil {
		return Escalation{}
	}
	req, gated := Classify(name, args, g.Workspace)
	if !gated || req.Kind != KindShell {
		return Escalation{}
	}
	card := Approval{ID: callID, Tool: name, Kind: KindEscalate, Command: req.Command,
		Reasons: []string{"blocked by the sandbox"}, Destructive: req.Destructive,
		Choices: []string{ReplyOnce, ReplyDeny}, State: StatePending}
	grant := ""
	if !req.Destructive {
		grant = grantFor(sandbox.BlockedPath(b.Output), b.Policy)
	}
	if grant != "" {
		card.Path = grant
		card.Remember = []Rule{{Tool: ToolWrite, Pattern: grant, Action: ActionAllow}}
		card.Choices = []string{ReplySession, ReplyFolder, ReplyGlobal, ReplyOnce, ReplyDeny}
	}
	reply, err := g.ask(ctx, card, notify)
	if err != nil || reply == ReplyDeny {
		return Escalation{}
	}
	if reply == ReplyOnce || grant == "" {
		return Escalation{Run: true}
	}
	g.remember(reply, card.Remember)
	pol := *b.Policy
	pol.Granted = append(append([]string(nil), b.Policy.Granted...), grant)
	return Escalation{Run: true, Sandbox: &pol}
}

func defaultHomeDir() (string, error) { return os.UserHomeDir() }

// homeDir is swapped in tests.
var homeDir = defaultHomeDir

// grantFor is the folder to offer for a blocked path: its parent, or
// the path itself when the parent is too broad to hand out (/, a
// top-level dir, the home folder or its parent). "" when there is no
// path or the policy already covers it, so the block had another cause.
func grantFor(path string, pol *sandbox.Policy) string {
	if path == "" || !filepath.IsAbs(path) {
		return ""
	}
	path = filepath.Clean(path)
	roots := append(append([]string(nil), pol.Granted...), pol.Writable...)
	for _, r := range roots {
		if pathMatches(r, path) {
			return ""
		}
	}
	dir := filepath.Dir(path)
	home, _ := homeDir()
	if dir == "/" || filepath.Dir(dir) == "/" || (home != "" && (dir == home || dir == filepath.Dir(home))) {
		return path
	}
	return dir
}

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

func (g *Gate) policy(req Request, mode sandbox.Mode) *sandbox.Policy {
	if req.Kind != KindShell || mode == sandbox.ModeNone || g.SandboxKind == "" {
		return nil
	}
	return &sandbox.Policy{Mode: mode, Writable: append([]string{g.Workspace}, g.ExtraWritable...), Granted: g.granted()}
}

// granted are the folders allow-write rules open to sandboxed commands:
// one notion of "where the agent may write" for every tool. "*" never
// lifts the sandbox, and a folder overlapping a deny-write rule stays
// shut (Landlock cannot carve a denied hole out of a granted folder).
func (g *Gate) granted() []string {
	if g.Store == nil {
		return nil
	}
	rules := g.Store.Effective(g.Workspace, g.SessionID)
	var out []string
	for _, r := range rules {
		if r.Tool != ToolWrite || r.Action != ActionAllow || !filepath.IsAbs(r.Pattern) {
			continue
		}
		denied := false
		for _, d := range rules {
			if d.Tool == ToolWrite && d.Action == ActionDeny && (pathMatches(d.Pattern, r.Pattern) || pathMatches(r.Pattern, d.Pattern)) {
				denied = true
				break
			}
		}
		if !denied {
			out = append(out, r.Pattern)
		}
	}
	return out
}

func describe(req Request) string {
	switch req.Kind {
	case KindWrite:
		return "write to " + req.Path
	case KindMCP:
		return "MCP tool " + req.Server + "/" + req.MCPTool
	}
	return "command `" + strings.TrimSpace(req.Command) + "`"
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

// applyHookVerdict lets a hook skip a prompt ("allow") or force one
// ("ask"). Deny rules still win, and an allowed call keeps its sandbox.
func applyHookVerdict(ctx context.Context, d Decision) Decision {
	verdict, _ := ctx.Value(hookVerdictKey{}).(string)
	switch {
	case verdict == HookAllow && d.Verdict == Ask:
		d.Verdict = Allow
	case verdict == HookAsk && d.Verdict == Allow:
		d.Verdict = Ask
		d.Reasons = []string{"a hook asked for confirmation"}
		d.Choices = []string{ReplyOnce, ReplyDeny}
		d.Remember = nil
	}
	return d
}
