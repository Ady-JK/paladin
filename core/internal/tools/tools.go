// Package tools owns the model-facing tools (read, write, edit, shell,
// ask, todo, skill, session_search) and the registry that serves them to
// the agent loop. fileengine and scope are the shared file-safety layer
// behind write and edit.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"sort"
	"strings"

	"github.com/Ady-JK/paladin/core/internal/jsonx"
	"github.com/Ady-JK/paladin/core/internal/media"
	"github.com/Ady-JK/paladin/core/internal/redact"
	"github.com/Ady-JK/paladin/core/internal/sandbox"
)

// ToolCtx is per-turn tool context.
type ToolCtx struct {
	Workspace string
	// Project is the folder project-scoped state (folder memory) keys
	// on; it differs from Workspace for worktree sessions.
	Project    string
	SessionDir string
	Cancel     context.Context
	// SessionID and CallID identify the running ask call: the agent sets
	// SessionID once per turn and the registry sets CallID per call so
	// the ask tool can rendezvous the blocked call with the UI answer.
	SessionID string
	CallID    string
	// Sandbox confines shell commands; nil runs them unconfined.
	Sandbox *sandbox.Policy
}

// ToolDefinition is the tool schema sent to the model.
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
	Image   *media.Image
	IsError bool
}

// RichOutput lets a tool provide an image without embedding its bytes in
// text, JSONL events, or the transcript.
type RichOutput struct {
	Content string
	Image   *media.Image
}

// RichTool is optional; existing text tools keep their Call method.
type RichTool interface {
	CallRich(args json.RawMessage, ctx *ToolCtx) (RichOutput, error)
}

// ToolError is a failed tool call. Failed carries model-facing output;
// its message is the human summary.
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

// Filter returns a registry over the tools keep accepts, sharing the
// same instances.
func (r *Registry) Filter(keep func(name string) bool) *Registry {
	out := NewRegistry()
	for n, t := range r.tools {
		if keep(n) {
			out.tools[n] = t
		}
	}
	return out
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

// ParallelSafe is implemented by tools without side effects that may
// run concurrently with other parallel-safe calls in the same round.
// Tools that don't implement it are exclusive.
type ParallelSafe interface {
	ParallelSafe() bool
}

// ParallelSafe reports whether the named tool may run concurrently.
func (r *Registry) ParallelSafe(name string) bool {
	resolved, ok := r.resolveName(name)
	if !ok {
		return false
	}
	p, ok := r.tools[resolved].(ParallelSafe)
	return ok && p.ParallelSafe()
}

// Mutating is implemented by tools that may change files in the
// workspace, so the harness can snapshot around them for undo.
type Mutating interface {
	Mutates() bool
}

// Mutates reports whether the named tool may change workspace files.
func (r *Registry) Mutates(name string) bool {
	resolved, ok := r.resolveName(name)
	if !ok {
		return false
	}
	m, ok := r.tools[resolved].(Mutating)
	return ok && m.Mutates()
}

// MCPInfo describes a tool proxied from an MCP server; the hints come
// from the server's tool annotations.
type MCPInfo struct {
	Server      string
	Tool        string
	ReadOnly    bool
	Destructive bool
}

// MCPTool is implemented by tools that proxy an MCP server.
type MCPTool interface {
	MCPInfo() MCPInfo
}

// MCPInfo reports whether the named tool proxies an MCP server.
func (r *Registry) MCPInfo(name string) (MCPInfo, bool) {
	resolved, ok := r.resolveName(name)
	if !ok {
		return MCPInfo{}, false
	}
	m, ok := r.tools[resolved].(MCPTool)
	if !ok {
		return MCPInfo{}, false
	}
	return m.MCPInfo(), true
}

// With returns a copy of the registry plus extra tools, sharing the
// existing instances. An extra never replaces a tool already present.
func (r *Registry) With(extra ...Tool) *Registry {
	out := NewRegistry()
	for n, t := range r.tools {
		out.tools[n] = t
	}
	for _, t := range extra {
		if _, taken := out.tools[t.Name()]; !taken {
			out.tools[t.Name()] = t
		}
	}
	return out
}

func (r *Registry) Call(call *ToolCall, ctx *ToolCtx) (result ToolResult) {
	result = ToolResult{CallID: call.ID, Name: call.Name}
	name, ok := r.resolveName(call.Name)
	if !ok {
		return errorResult(result, fmt.Sprintf("unknown tool %q; available tools: %s", call.Name, strings.Join(r.Names(), ", ")))
	}
	result.Name = name
	tool := r.tools[name]
	var args json.RawMessage
	if len(call.Arguments) == 0 || isBlank(call.Arguments) {
		args = json.RawMessage("null")
	} else if fixed, ok := repairArgs(call.Arguments); !ok {
		return errorResult(result, "the arguments are not valid JSON (possibly truncated); resend the complete JSON object")
	} else {
		args = json.RawMessage(fixed)
	}
	// A tool bug must never take down the sidecar: the panic becomes a
	// model-visible failure and the turn carries on.
	defer func() {
		if p := recover(); p != nil {
			fmt.Fprintf(os.Stderr, "tool %s panicked: %v\n%s", name, p, debug.Stack())
			result = errorResult(ToolResult{CallID: call.ID, Name: name}, fmt.Sprintf("tool %s crashed: %v", name, p))
		}
	}()
	var content string
	var err error
	if rich, ok := tool.(RichTool); ok {
		var output RichOutput
		output, err = rich.CallRich(args, ctxWithCall(ctx, call.ID))
		content, result.Image = output.Content, output.Image
	} else {
		content, err = tool.Call(args, ctxWithCall(ctx, call.ID))
	}
	if err != nil {
		if terr := (*ToolError)(nil); errors.As(err, &terr) {
			result.Content = terr.ResultContent()
		} else {
			result.Content = string(jsonx.MustMarshal(map[string]string{"error": err.Error()}))
		}
		result.Content = redact.String(result.Content)
		result.Image = nil
		result.IsError = true
		return result
	}
	result.Content = content
	return result
}

// Prepare resolves the canonical tool name and repaired arguments the
// way Call will, so a permission gate judges exactly what would run.
func (r *Registry) Prepare(call *ToolCall) (string, json.RawMessage, bool) {
	name, ok := r.resolveName(call.Name)
	if !ok {
		return "", nil, false
	}
	if len(call.Arguments) == 0 || isBlank(call.Arguments) {
		return name, json.RawMessage("null"), true
	}
	fixed, ok := repairArgs(call.Arguments)
	if !ok {
		return "", nil, false
	}
	return name, json.RawMessage(fixed), true
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
// shell, ask, todo. fileengine and scope stay shared modules behind
// write/edit. todo persists per-session, needing no shared instance.
func BuiltinCatalog() *Catalog {
	c := NewCatalog()
	c.Register(Template{Name: "read", New: func() Tool { return ReadTool{} }})
	c.Register(Template{Name: "write", New: func() Tool { return NewWriteTool(NewEngine()) }})
	c.Register(Template{Name: "edit", New: func() Tool { return NewEditTool(NewEngine()) }})
	c.Register(Template{Name: "shell", New: func() Tool { return NewShellTool() }})
	c.Register(Template{Name: "ask", New: func() Tool { return NewAskTool() }})
	c.Register(Template{Name: "browser", New: func() Tool { return NewBrowserTool() }})
	c.Register(Template{Name: "todo", New: func() Tool { return TodoTool{} }})
	return c
}

// BuiltinRegistry is the model-facing tool registry.
func BuiltinRegistry() *Registry {
	return BuiltinCatalog().Instantiate()
}
