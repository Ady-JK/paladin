package permission

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/Ady-JK/paladin/core/internal/sandbox"
)

// Approval states.
const (
	StatePending  = "pending"
	StateApproved = "approved"
	StateDenied   = "denied"
)

// Approval is the chat/approval event payload: one card in the UI.
type Approval struct {
	ID          string   `json:"id"`
	Tool        string   `json:"tool"`
	Kind        string   `json:"kind"`
	Command     string   `json:"command,omitempty"`
	Path        string   `json:"path,omitempty"`
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
	case ReplyOnce, ReplySession, ReplyFolder, ReplyDeny:
	default:
		return fmt.Errorf("decision must be once, session, folder or deny, got %q", reply)
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
	if !gated {
		return Auth{Run: true}
	}
	var rules []Rule
	if g.Store != nil {
		rules = g.Store.Effective(g.Workspace, g.SessionID)
	}
	d := Decide(g.Preset, req, rules)
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
		Reasons: d.Reasons, Destructive: d.Destructive, Choices: d.Choices, Remember: d.Remember, State: StatePending}
	reply, err := g.ask(ctx, card, notify)
	if err != nil {
		return Auth{Content: refusal("interrupted while waiting for the user's approval; it did not run")}
	}
	if reply == ReplyDeny {
		return Auth{Content: refusal("denied by the user: they declined this " + describe(req) + ". Do not retry it; continue another way or ask the user what they want.")}
	}
	if (reply == ReplySession || reply == ReplyFolder) && g.Store != nil && len(d.Remember) > 0 {
		scope := ScopeSession
		if reply == ReplyFolder {
			scope = ScopeFolder
		}
		_ = g.Store.Add(scope, g.Workspace, g.SessionID, d.Remember...)
	}
	return Auth{Run: true, Sandbox: g.policy(req, d.Sandbox)}
}

// Escalate asks to rerun a shell call outside the sandbox after the
// sandbox blocked it. It reports whether the user agreed.
func (g *Gate) Escalate(ctx context.Context, callID, name string, args json.RawMessage, notify func(Approval)) bool {
	if g.Preset == FullAccess || g.SandboxKind == "" {
		return false
	}
	req, gated := Classify(name, args, g.Workspace)
	if !gated || req.Kind != KindShell {
		return false
	}
	card := Approval{ID: callID, Tool: name, Kind: KindEscalate, Command: req.Command,
		Reasons: []string{"blocked by the sandbox"}, Destructive: req.Destructive,
		Choices: []string{ReplyOnce, ReplyDeny}, State: StatePending}
	reply, err := g.ask(ctx, card, notify)
	return err == nil && reply != ReplyDeny
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
	return &sandbox.Policy{Mode: mode, Writable: append([]string{g.Workspace}, g.ExtraWritable...)}
}

func describe(req Request) string {
	if req.Kind == KindWrite {
		return "write to " + req.Path
	}
	return "command `" + strings.TrimSpace(req.Command) + "`"
}
