package inference

import "fmt"

const missingAnthropicResult = "Tool execution did not complete or its output was not recorded; the result is unknown. Do not assume it succeeded."

// pairAnthropicTools enforces the Messages API's adjacency contract at the
// wire boundary, including live rounds that did not pass through transcript
// repair. A tool_use batch gets exactly one following user message containing
// all its results first, then any reminders/steering/images that arrived among
// them. Missing results are explicit errors, never fabricated successes.
//
// Matching stops at the next assistant message: pulling a later result across
// another model response would rewrite causality. Orphan/duplicate results
// become labelled user context rather than invalid protocol blocks. Signed
// thinking and tool_use blocks are left untouched.
func pairAnthropicTools(in []anthropicMessage) []anthropicMessage {
	out := make([]anthropicMessage, 0, len(in))
	for i := 0; i < len(in); i++ {
		m := in[i]
		if m.Role != "assistant" || !hasToolUse(m) {
			content := make([]anthropicBlock, 0, len(m.Content))
			for _, block := range m.Content {
				content = append(content, anthropicResultContext(block))
			}
			out = append(out, anthropicMessage{Role: m.Role, Content: content})
			continue
		}

		out = append(out, m)
		answered := map[string]bool{}
		var order []string
		for _, block := range m.Content {
			if block.Type == "tool_use" {
				if _, seen := answered[block.ID]; !seen {
					order = append(order, block.ID)
					answered[block.ID] = false
				}
			}
		}
		var results, context []anthropicBlock
		j := i + 1
		for ; j < len(in) && in[j].Role == "user"; j++ {
			for _, block := range in[j].Content {
				done, expected := answered[block.ToolUseID]
				if block.Type == "tool_result" && expected && !done {
					answered[block.ToolUseID] = true
					results = append(results, block)
				} else {
					context = append(context, anthropicResultContext(block))
				}
			}
		}
		for _, id := range order {
			if !answered[id] {
				results = append(results, anthropicBlock{Type: "tool_result", ToolUseID: id, Content: missingAnthropicResult, IsError: true})
			}
		}
		out = append(out, anthropicMessage{Role: "user", Content: append(results, context...)})
		i = j - 1
	}
	return out
}

func anthropicResultContext(block anthropicBlock) anthropicBlock {
	if block.Type != "tool_result" {
		return block
	}
	return anthropicBlock{Type: "text", Text: fmt.Sprintf("[Unmatched tool result %q]\n%s", block.ToolUseID, block.Content)}
}
