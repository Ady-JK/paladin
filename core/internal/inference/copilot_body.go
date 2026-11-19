// copilot_body.go holds the Copilot-only parts of a chat-completions request
// body, copied from the VS Code Copilot extension: the sampling defaults,
// normalised tool schemas, Gemini's function-calling mode, and the prompt
// cache breakpoints Copilot reads as copilot_cache_control.

package inference

import (
	"strings"

	"github.com/openai/openai-go"
)

const (
	copilotTemperature = 0.1
	copilotTopP        = 1
	// maxCacheBreakpoints is the number of cache markers a request may carry.
	maxCacheBreakpoints = 4
	noDescription       = "No description provided."
)

// applyCopilotChatParams sets what VS Code sends on every chat-completions
// call: temperature, top_p, n and max_tokens, plus Gemini 3's validated
// function-calling mode when tools are present.
func (p *OpenAIProvider) applyCopilotChatParams(params *openai.ChatCompletionNewParams, hasTools bool) {
	if !p.isCopilot {
		return
	}
	params.Temperature = openai.Float(copilotTemperature)
	params.TopP = openai.Float(copilotTopP)
	params.N = openai.Int(1)
	if p.meta.MaxOutput > 0 {
		params.MaxTokens = openai.Int(p.meta.MaxOutput)
	}
	if hasTools && strings.Contains(strings.ToLower(p.model), "gemini-3") {
		params.SetExtraFields(map[string]any{"tool_choice": "validated"})
	}
}

// normalizeCopilotTools repairs tool definitions the way VS Code does before
// sending them: an object schema with properties, a description, no top-level
// combinators, and nullable types spelled the way Gemini accepts.
func normalizeCopilotTools(model string, tools []ToolDefinition) []ToolDefinition {
	gemini := strings.Contains(strings.ToLower(model), "gemini")
	out := make([]ToolDefinition, len(tools))
	for i, t := range tools {
		schema := map[string]any{}
		for k, v := range schemaMap(t.Parameters) {
			schema[k] = v
		}
		schema["type"] = "object"
		if _, ok := schema["properties"].(map[string]any); !ok {
			schema["properties"] = map[string]any{}
		}
		for _, k := range []string{"oneOf", "anyOf", "allOf", "not", "if", "then", "else"} {
			delete(schema, k)
		}
		if gemini {
			nullableToFlag(schema)
		}
		desc := t.Description
		if strings.TrimSpace(desc) == "" {
			desc = noDescription
		}
		out[i] = ToolDefinition{Name: t.Name, Description: desc, Parameters: schema}
	}
	return out
}

// nullableToFlag rewrites ["x","null"] types to x with nullable:true, in
// every property and array item below node.
func nullableToFlag(node map[string]any) {
	if types, ok := node["type"].([]any); ok {
		var kept []any
		nullable := false
		for _, t := range types {
			if t == "null" {
				nullable = true
			} else {
				kept = append(kept, t)
			}
		}
		if nullable && len(kept) == 1 {
			node["type"], node["nullable"] = kept[0], true
		}
	}
	if props, ok := node["properties"].(map[string]any); ok {
		for _, v := range props {
			if child, ok := v.(map[string]any); ok {
				nullableToFlag(child)
			}
		}
	}
	if items, ok := node["items"].(map[string]any); ok {
		nullableToFlag(items)
	}
}

// chatRole names a message union's role.
func chatRole(m openai.ChatCompletionMessageParamUnion) string {
	switch {
	case m.OfSystem != nil:
		return "system"
	case m.OfUser != nil:
		return "user"
	case m.OfAssistant != nil:
		return "assistant"
	case m.OfTool != nil:
		return "tool"
	}
	return ""
}

// chatCacheable reports whether m has content a breakpoint can attach to.
func chatCacheable(m openai.ChatCompletionMessageParamUnion) bool {
	switch {
	case m.OfSystem != nil:
		return m.OfSystem.Content.OfString.Value != "" || len(m.OfSystem.Content.OfArrayOfContentParts) > 0
	case m.OfUser != nil:
		return m.OfUser.Content.OfString.Value != "" || len(m.OfUser.Content.OfArrayOfContentParts) > 0
	case m.OfAssistant != nil:
		return m.OfAssistant.Content.OfString.Value != ""
	case m.OfTool != nil:
		return m.OfTool.Content.OfString.Value != "" || len(m.OfTool.Content.OfArrayOfContentParts) > 0
	}
	return false
}

// markCacheBreakpoint adds copilot_cache_control to m without disturbing any
// extra fields it already carries (replayed reasoning, for one).
func markCacheBreakpoint(m openai.ChatCompletionMessageParamUnion) {
	marker := map[string]any{"type": "ephemeral"}
	merge := func(existing map[string]any) map[string]any {
		out := map[string]any{"copilot_cache_control": marker}
		for k, v := range existing {
			out[k] = v
		}
		return out
	}
	switch {
	case m.OfSystem != nil:
		m.OfSystem.SetExtraFields(merge(m.OfSystem.ExtraFields()))
	case m.OfUser != nil:
		m.OfUser.SetExtraFields(merge(m.OfUser.ExtraFields()))
	case m.OfAssistant != nil:
		m.OfAssistant.SetExtraFields(merge(m.OfAssistant.ExtraFields()))
	case m.OfTool != nil:
		m.OfTool.SetExtraFields(merge(m.OfTool.ExtraFields()))
	}
}

func hasToolCalls(m openai.ChatCompletionMessageParamUnion) bool {
	return m.OfAssistant != nil && len(m.OfAssistant.ToolCalls) > 0
}

// addChatCacheBreakpoints places up to four cache breakpoints with VS Code's
// strategy for Chat Completions. The prompt owns two: the user's current
// prompt and the newest tool result. The rest are filled walking back from the
// end: the last tool result of each earlier round and the prompt above it,
// below the current prompt; above it, only assistant answers that made no
// tool calls. Anything left goes to the leading system and user messages.
// A new prompt always misses once, because the old tail moves above it; inside
// a tool loop each round hits the previous tool result.
func addChatCacheBreakpoints(msgs []openai.ChatCompletionMessageParamUnion) {
	marked := make([]bool, len(msgs))
	budget := maxCacheBreakpoints
	mark := func(i int) {
		marked[i] = true
		markCacheBreakpoint(msgs[i])
		budget--
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if chatRole(msgs[i]) == "user" {
			if chatCacheable(msgs[i]) {
				mark(i)
			}
			break
		}
	}
	if n := len(msgs); n > 0 && chatRole(msgs[n-1]) == "tool" && !marked[n-1] && chatCacheable(msgs[n-1]) {
		mark(n - 1)
	}

	belowCurrentPrompt := true
	for i := len(msgs) - 1; i >= 0 && budget > 0; i-- {
		if marked[i] {
			continue
		}
		role := chatRole(msgs[i])
		lastToolOfRound := role == "tool" && (i == len(msgs)-1 || chatRole(msgs[i+1]) != "tool")
		answer := role == "assistant" && !hasToolCalls(msgs[i])
		if (belowCurrentPrompt && (lastToolOfRound || role == "user") || answer) && chatCacheable(msgs[i]) {
			mark(i)
		}
		if role == "user" {
			belowCurrentPrompt = false
		}
	}
	for i := 0; i < len(msgs) && budget > 0; i++ {
		role := chatRole(msgs[i])
		if (role == "user" || role == "system") && !marked[i] && chatCacheable(msgs[i]) {
			mark(i)
		}
		if role != "user" && role != "system" {
			break
		}
	}
}
