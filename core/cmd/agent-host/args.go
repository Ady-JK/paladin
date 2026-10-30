package main

import (
	"encoding/json"
	"fmt"

	"github.com/Ady-JK/paladin/core/internal/agent"
	"github.com/Ady-JK/paladin/core/internal/runtime"
)

// args are a request's params, decoded once. Field accessors are
// lenient: a missing or wrongly typed field reads as the zero value (or
// the given default) instead of failing the whole request.
type args struct {
	raw    json.RawMessage
	fields map[string]json.RawMessage
}

func parseArgs(raw json.RawMessage) args {
	a := args{raw: raw}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &a.fields)
	}
	return a
}

func (a args) str(key string) string {
	var s string
	if v, ok := a.fields[key]; ok {
		_ = json.Unmarshal(v, &s)
	}
	return s
}

func (a args) flag(key string) bool {
	var b bool
	if v, ok := a.fields[key]; ok && json.Unmarshal(v, &b) == nil {
		return b
	}
	return false
}

func (a args) num(key string, def int) int {
	var n float64
	if v, ok := a.fields[key]; ok && json.Unmarshal(v, &n) == nil {
		return int(n)
	}
	return def
}

// decode unmarshals the whole params object into v; empty params leave
// v untouched. Unlike the field accessors it is strict: a malformed
// object fails the request instead of running on partial input.
func (a args) decode(v any) error {
	if len(a.raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(a.raw, v); err != nil {
		return fmt.Errorf("invalid params: %w", err)
	}
	return nil
}

func (a args) openOpts() runtime.OpenOpts {
	return runtime.OpenOpts{Worktree: a.flag("worktree"), Name: a.str("name")}
}

// ChatParams are submitChat's params.
type ChatParams struct {
	SessionID    string  `json:"sessionId"`
	Message      string  `json:"message"`
	Cwd          string  `json:"cwd"`
	Provider     *string `json:"provider"`
	Model        *string `json:"model"`
	SystemPrompt *string `json:"systemPrompt"`
	Resume       bool    `json:"resume"`
	Skill        *string `json:"skill"`
	// Display is how the client rendered the message; echoed in turn
	// info so a re-attaching UI can redraw the bubble.
	Display string `json:"display"`
	// Command runs a prompt command (`/review`) with Message as args.
	Command *string `json:"command"`
	// Mode is "build" (default) or "plan".
	Mode string `json:"mode"`
	// Compact runs a manual compaction instead of a model turn.
	Compact bool `json:"compact"`
	// Permissions is "read-only", "auto" (default) or "full-access".
	Permissions string `json:"permissions"`
}

func (p ChatParams) request() agent.Request {
	return agent.Request{
		SessionID: p.SessionID, Message: p.Message, Cwd: p.Cwd,
		Provider: p.Provider, Model: p.Model,
		SystemPrompt: p.SystemPrompt, Resume: p.Resume,
		Skill: p.Skill, Command: p.Command, Mode: p.Mode,
		Compact: p.Compact, Display: p.Display,
		Permissions: p.Permissions,
	}
}

func (a args) chat() (ChatParams, error) {
	var p ChatParams
	err := a.decode(&p)
	return p, err
}
