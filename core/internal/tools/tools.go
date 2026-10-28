// Package tools owns the model-facing tool registry: read, write,
// edit, shell. fileengine and scope stay shared modules behind
// write/edit.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/Ady-JK/paladin/core/internal/jsonx"
)

// ToolCtx is per-turn tool context.
type ToolCtx struct {
	Workspace  string
	SessionDir string
	Cancel     context.Context
	// SessionID and CallID identify the running ask call: the agent sets
	// SessionID once per turn and the registry sets CallID per call so
	// the ask tool can rendezvous the blocked call with the UI answer.
	SessionID string
	CallID    string
}

// ToolDefinition mirrors the Rust shape sent to the model.
type ToolDefinition struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters"`
}

// ToolCall is one model-requested invocation.
type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

// ToolResult is one executed invocation.
type ToolResult struct {
	CallID  string
	Name    string
	Content string
	IsError bool
}

// ToolError mirrors the Rust ToolError. Failed carries model-facing
// output; its message is the human summary.
type ToolError struct {
	Kind    string
	Content string
	Msg     string
}

func (e *ToolError) Error() string {
	switch e.Kind {
	case "unknown":
		return "unknown tool: " + e.Msg
	case "args":
		return "bad arguments: " + e.Msg
	case "exec":
		return "execution failed: " + e.Msg
	default:
		return e.Msg
	}
}

// WithContent is a failure that preserves model-facing output.
func WithContent(content, msg string) *ToolError {
	return &ToolError{Kind: "failed", Content: content, Msg: msg}
}

func ArgsError(msg string) *ToolError {
	content, _ := json.Marshal(map[string]string{"error": msg})
	return &ToolError{Kind: "failed", Content: string(content), Msg: msg}
}

func (e *ToolError) ResultContent() string {
	if e.Kind == "failed" {
		return e.Content
	}
	content, _ := json.Marshal(map[string]string{"error": e.Error()})
	return string(content)
}

// Tool is one model-callable tool.
type Tool interface {
	Name() string
	Definition() ToolDefinition
	Call(args json.RawMessage, ctx *ToolCtx) (string, error)
}

// Registry dispatches calls by tool name.
type Registry struct {
	tools map[string]Tool
}

func NewRegistry() *Registry {
	return &Registry{tools: map[string]Tool{}}
}

func (r *Registry) Register(t Tool) {
	r.tools[t.Name()] = t
}

func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

func (r *Registry) Len() int {
	return len(r.tools)
}

func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.tools))
	for n := range r.tools {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Subset returns a registry over the named tools, sharing the same
// instances (important for the stateful shell tool). Empty names clones
// every tool. Unknown names are an error so template typos fail loudly.
func (r *Registry) Subset(names []string) (*Registry, error) {
	out := NewRegistry()
	if len(names) == 0 {
		for n, t := range r.tools {
			out.tools[n] = t
		}
		return out, nil
	}
	for _, name := range names {
		t, ok := r.tools[name]
		if !ok {
			return nil, fmt.Errorf("unknown tool %q", name)
		}
		out.tools[name] = t
	}
	return out, nil
}

func (r *Registry) Definitions() []ToolDefinition {
	names := r.Names()
	var out []ToolDefinition
	for _, n := range names {
		out = append(out, r.tools[n].Definition())
	}
	if out == nil {
		out = []ToolDefinition{}
	}
	return out
}

func (r *Registry) Call(call *ToolCall, ctx *ToolCtx) ToolResult {
	result := ToolResult{CallID: call.ID, Name: call.Name}
	tool, ok := r.tools[call.Name]
	if !ok {
		return errorResult(result, fmt.Sprintf("unknown tool %q", call.Name))
	}
	var args json.RawMessage
	if len(call.Arguments) == 0 || isBlank(call.Arguments) {
		args = json.RawMessage("null")
	} else if !json.Valid([]byte(call.Arguments)) {
		return errorResult(result, "the arguments are not valid JSON")
	} else {
		args = json.RawMessage(call.Arguments)
	}
	content, err := tool.Call(args, ctxWithCall(ctx, call.ID))
	if err != nil {
		if terr, ok := err.(*ToolError); ok {
			result.Content = terr.ResultContent()
		} else {
			result.Content = string(jsonx.MustMarshal(map[string]string{"error": err.Error()}))
		}
		result.IsError = true
		return result
	}
	result.Content = content
	return result
}

// ctxWithCall clones the turn context with the call id stamped in. Call
// is shared by every tool, so the clone keeps a running ask call from
// racing the next call's id on the same pointer.
func ctxWithCall(ctx *ToolCtx, callID string) *ToolCtx {
	if ctx == nil {
		return &ToolCtx{CallID: callID}
	}
	cp := *ctx
	cp.CallID = callID
	return &cp
}

func errorResult(result ToolResult, message string) ToolResult {
	result.Content = string(jsonx.MustMarshal(map[string]string{"error": message}))
	result.IsError = true
	return result
}

func isBlank(s string) bool {
	for _, r := range s {
		if r != ' ' && r != '\t' && r != '\n' && r != '\r' {
			return false
		}
	}
	return true
}

// Template is the blueprint for one tool: identity plus a constructor.
// A Catalog turns templates into live tool instances.
type Template struct {
	Name string
	New  func() Tool
}

// Catalog is the template registry for tools. New tool kinds are added
// by registering a template instead of editing a switch.
type Catalog struct {
	templates map[string]Template
}

func NewCatalog() *Catalog {
	return &Catalog{templates: map[string]Template{}}
}

func (c *Catalog) Register(t Template) {
	if t.Name == "" || t.New == nil {
		return
	}
	c.templates[t.Name] = t
}

func (c *Catalog) Names() []string {
	names := make([]string, 0, len(c.templates))
	for n := range c.templates {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Instantiate creates one live instance per registered template.
func (c *Catalog) Instantiate() *Registry {
	r := NewRegistry()
	for _, name := range c.Names() {
		r.Register(c.templates[name].New())
	}
	return r
}

// BuiltinCatalog is the shipped tool template set: read, write, edit,
// shell, ask. fileengine and scope stay shared modules behind write/edit.
func BuiltinCatalog() *Catalog {
	c := NewCatalog()
	c.Register(Template{Name: "read", New: func() Tool { return ReadTool{} }})
	c.Register(Template{Name: "write", New: func() Tool { return NewWriteTool(NewEngine()) }})
	c.Register(Template{Name: "edit", New: func() Tool { return NewEditTool(NewEngine()) }})
	c.Register(Template{Name: "shell", New: func() Tool { return NewShellTool() }})
	c.Register(Template{Name: "ask", New: func() Tool { return NewAskTool() }})
	return c
}

// BuiltinRegistry is the model-facing tool registry.
func BuiltinRegistry() *Registry {
	return BuiltinCatalog().Instantiate()
}
