// reason.go adds the mandatory "reason" argument to every Paladin tool:
// a one-line note of what the call does and why. It is injected into the
// schema the model sees (Definitions) and enforced before a call runs
// (CheckReason, Call), so no tool implements it. The tool itself never
// sees it: Call strips it after the check. MCP and extension tools are
// third-party schemas and are left alone.

package tools

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/Ady-JK/paladin/core/internal/jsonx"
)

// ReasonParam is the argument name every Paladin tool requires.
const ReasonParam = "reason"

const reasonDescription = "Required. A one-line note of what this specific call does and why it is run, " +
	"e.g. \"Running the Go tests to confirm the retry fix\" or \"Reading config.go to find where timeouts are set\"."

// takesReason reports whether the resolved tool carries the reason
// argument: every tool except MCP and extension ones.
func (r *Registry) takesReason(resolved string) bool {
	t, ok := r.tools[resolved]
	if !ok {
		return false
	}
	if _, mcp := t.(MCPTool); mcp {
		return false
	}
	return true
}

// TakesReason reports whether the named tool requires a reason.
func (r *Registry) TakesReason(name string) bool {
	resolved, ok := r.resolveName(name)
	return ok && r.takesReason(resolved)
}

// schemaOf is the argument schema the model was shown for a resolved
// tool name, reason included, so coercion and leak recovery see it.
func (r *Registry) schemaOf(resolved string) any {
	def := r.tools[resolved].Definition()
	if r.takesReason(resolved) {
		def = withReason(def)
	}
	return def.Parameters
}

// withReason returns def with a required string "reason" property added.
// The original parameters are not modified.
func withReason(def ToolDefinition) ToolDefinition {
	schema := map[string]any{}
	if def.Parameters != nil {
		data, err := json.Marshal(def.Parameters)
		if err != nil || json.Unmarshal(data, &schema) != nil || schema == nil {
			schema = map[string]any{}
		}
	}
	if _, ok := schema["type"]; !ok {
		schema["type"] = "object"
	}
	props, _ := schema["properties"].(map[string]any)
	if props == nil {
		props = map[string]any{}
	}
	props[ReasonParam] = map[string]any{"type": "string", "description": reasonDescription}
	schema["properties"] = props
	required := stringList(schema["required"])
	if !slices.Contains(required, ReasonParam) {
		required = append(required, ReasonParam)
	}
	schema["required"] = required
	def.Parameters = schema
	return def
}

// reasonOf returns the trimmed reason in a call's arguments.
func reasonOf(args json.RawMessage) string {
	var a struct {
		Reason any `json:"reason"`
	}
	if json.Unmarshal(args, &a) != nil {
		return ""
	}
	s, _ := a.Reason.(string)
	return strings.TrimSpace(s)
}

func missingReason(result ToolResult) ToolResult {
	return errorResult(result, fmt.Sprintf("missing required argument %q: add a one-line note of what this call does "+
		"and why you run it, then resend the call", ReasonParam))
}

// CheckReason refuses a call to a tool that requires a reason when the
// reason is missing or blank. refused is true when it lacks one; result
// is then the refusal to hand the model. Unknown tools and bad JSON pass
// through: Call reports those itself.
func (r *Registry) CheckReason(call *ToolCall) (result ToolResult, refused bool) {
	name, args, ok := r.Prepare(call)
	if !ok || !r.takesReason(name) {
		return ToolResult{}, false
	}
	if reasonOf(args) == "" {
		return missingReason(ToolResult{CallID: call.ID, Name: name}), true
	}
	return ToolResult{}, false
}

// WithoutReason drops the reason from a call's arguments so two calls
// that differ only in their wording compare equal (the doom-loop guard).
// Tools that take no reason keep their arguments unchanged.
func (r *Registry) WithoutReason(name, args string) string {
	if !r.TakesReason(name) {
		return args
	}
	return string(stripReason(json.RawMessage(args)))
}

// stripReason removes the reason from a JSON object; anything else is
// returned unchanged.
func stripReason(args json.RawMessage) json.RawMessage {
	var obj map[string]json.RawMessage
	if json.Unmarshal(args, &obj) != nil || obj == nil {
		return args
	}
	if _, ok := obj[ReasonParam]; !ok {
		return args
	}
	delete(obj, ReasonParam)
	out, err := jsonx.Marshal(obj)
	if err != nil {
		return args
	}
	return out
}

// KeepReason returns the arguments a hook rewrote a call to, with the
// call's own reason carried over when the rewrite left it out, so a
// hook that edits one field does not make the call refused.
func (r *Registry) KeepReason(call *ToolCall, updated json.RawMessage) string {
	name, before, ok := r.Prepare(call)
	if !ok || !r.takesReason(name) || reasonOf(updated) != "" {
		return string(updated)
	}
	reason := reasonOf(before)
	var obj map[string]json.RawMessage
	if reason == "" || json.Unmarshal(updated, &obj) != nil || obj == nil {
		return string(updated)
	}
	obj[ReasonParam] = jsonx.MustMarshal(reason)
	out, err := jsonx.Marshal(obj)
	if err != nil {
		return string(updated)
	}
	return string(out)
}

// WithDefaultReason fills in reason when args (a JSON object, or empty)
// carry none. It serves callers that are not a model, such as plugin
// panels, whose calls must still satisfy the mandatory argument.
func WithDefaultReason(args json.RawMessage, reason string) json.RawMessage {
	if reasonOf(args) != "" {
		return args
	}
	obj := map[string]json.RawMessage{}
	if len(args) > 0 && !isBlank(string(args)) && string(args) != "null" {
		if json.Unmarshal(args, &obj) != nil || obj == nil {
			return args
		}
	}
	obj[ReasonParam] = jsonx.MustMarshal(reason)
	out, err := jsonx.Marshal(obj)
	if err != nil {
		return args
	}
	return out
}
