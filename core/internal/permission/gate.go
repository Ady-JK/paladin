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
	Server  string `json:"server,omitempty"`
	MCPTool string `json:"mcpTool,omitempty"`
	Args    string `json:"args,omitempty"`
	// Host and URL describe a web request.
	Host        string   `json:"host,omitempty"`
	URL         string   `json:"url,omitempty"`
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
	// Readable are folders the session owns (its own data folder with
	// spilled tool output, job logs and screenshots): reads there run
	// without a card. Deny rules still apply.
	Readable []string
	// MCP identifies tools proxied from MCP servers; nil means none.
	MCP func(name string) (tools.MCPInfo, bool)
	// Plan asks once/deny for every MCP call (a server cannot prove a
	// tool is read-only); only deny rules skip the question.
	Plan bool
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

// Authorize decides one call, asking the user (and blocking) if needed.
func (g *Gate) Authorize(ctx context.Context, callID, name string, args json.RawMessage, notify func(Approval)) Auth {
	// Browser actions and visual generation may change state without a file
	// path or shell command, so read-only mode blocks their whole tool surface.
	if g.Preset == ReadOnly && (name == "browser" || name == "visual") {
		return Auth{Content: refusal("read-only mode: switch to Full Access to use " + name)}
	}
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
	if d.Verdict == Ask && req.Kind == KindRead && g.owns(req) {
		d = Decision{Verdict: Allow}
	}
	d = applyHookVerdict(ctx, d)
	if g.Plan && req.Kind == KindMCP && d.Verdict != Deny {
		d = Decision{Verdict: Ask, Destructive: req.Destructive,
			Reasons: []string{"plan mode: the server's read-only claim cannot be verified"},
			Choices: []string{ReplyOnce, ReplyDeny}}
	}
	switch d.Verdict {
	case Deny:
		if d.Rule == nil && len(d.Reasons) > 0 {
			return Auth{Content: refusal(strings.Join(d.Reasons, "; "))}
		}
		what := "this call"
		if d.Rule != nil {
			what = fmt.Sprintf("%s rule %q", d.Rule.Tool, d.Rule.Pattern)
		}
		if req.Kind == KindMCP {
			what = describe(req) + ", " + what
		}
		return Auth{Content: refusal("blocked by a permission rule (" + what + "). Do not retry it; choose another approach or ask the user.")}
	case Allow:
		return Auth{Run: true}
	}
	card := Approval{ID: callID, Tool: name, Kind: req.Kind, Command: req.Command, Path: req.Path,
		Server: req.Server, MCPTool: req.MCPTool, Args: req.Args, Host: req.Host, URL: req.URL,
		Reasons: d.Reasons, Destructive: d.Destructive, Choices: d.Choices, Remember: d.Remember, State: StatePending}
	reply, err := g.ask(ctx, card, notify)
	if err != nil {
		return Auth{Content: refusal("interrupted while waiting for the user's approval; it did not run")}
	}
	if reply == ReplyDeny {
		return Auth{Content: refusal("denied by the user: they declined this " + describe(req) + ". Do not retry it; continue another way or ask the user what they want.")}
	}
	g.remember(reply, d.Remember)
	return Auth{Run: true}
}

// owns reports whether a read stays inside one of the session's own
// folders, by its symlink-resolved path.
func (g *Gate) owns(req Request) bool {
	real := req.Real
	if real == "" {
		real = tools.RealPath(req.Path)
	}
	for _, dir := range g.Readable {
		if dir != "" && pathMatches(tools.RealPath(dir), real) {
			return true
		}
	}
	return false
}

// remember saves the rules a session/folder/global reply stands for.
func (g *Gate) remember(reply string, rules []Rule) {
	scope := map[string]string{ReplySession: ScopeSession, ReplyFolder: ScopeFolder, ReplyGlobal: ScopeGlobal}[reply]
	if scope == "" || g.Store == nil || len(rules) == 0 {
		return
	}
	_ = g.Store.Add(scope, g.Workspace, g.SessionID, rules...)
}

func defaultHomeDir() (string, error) { return os.UserHomeDir() }

// homeDir is swapped in tests.
var homeDir = defaultHomeDir

// TrustProject decides whether tool, which the workspace itself ships (a
// formatter or language server in node_modules/.bin or a virtualenv, or
// a server that runs the project's build scripts), may run. It asks once
// per folder: the answer is kept for the session or the folder, and a
// refusal for the session, so it does not ask again. Full access trusts
// everything a deny rule does not stop.
func (g *Gate) TrustProject(ctx context.Context, callID, tool string, notify func(Approval)) bool {
	if g.Preset == ReadOnly {
		return false
	}
	var rules []Rule
	if g.Store != nil {
		rules = g.Store.Effective(g.Workspace, g.SessionID)
	}
	var allowed bool
	for _, r := range rules {
		if r.Tool != ToolProject || !pathMatches(r.Pattern, g.Workspace) {
			continue
		}
		if r.Action == ActionDeny {
			return false
		}
		allowed = true
	}
	if allowed || g.Preset == FullAccess {
		return true
	}
	allow := Rule{Tool: ToolProject, Pattern: g.Workspace, Action: ActionAllow}
	card := Approval{ID: callID, Tool: "project tools", Kind: KindProject, Command: tool, Path: g.Workspace,
		Reasons: []string{"this project ships it, so it runs code from the project itself"},
		Choices: []string{ReplySession, ReplyFolder, ReplyDeny}, Remember: []Rule{allow}, State: StatePending}
	reply, err := g.ask(ctx, card, notify)
	if err != nil {
		return false
	}
	if reply == ReplyDeny {
		if g.Store != nil {
			_ = g.Store.Add(ScopeSession, g.Workspace, g.SessionID, Rule{Tool: ToolProject, Pattern: g.Workspace, Action: ActionDeny})
		}
		return false
	}
	g.remember(reply, card.Remember)
	return true
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

func describe(req Request) string {
	switch req.Kind {
	case KindWrite:
		return "write to " + req.Path
	case KindMCP:
		return "MCP tool " + req.Server + "/" + req.MCPTool
	case KindRead:
		return "read " + req.Path
	case KindWeb:
		return "visit to " + req.Host
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
// ("ask"). Deny rules still win.
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
