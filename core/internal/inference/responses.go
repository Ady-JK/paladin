// responses.go implements the OpenAI Responses dialect
// used by the OpenCode Go models that do not accept chat completions.

package inference

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/responses"
	"github.com/openai/openai-go/shared"
)

func toResponsesInput(messages []LlmMessage) responses.ResponseInputParam {
	out := make(responses.ResponseInputParam, 0, len(messages))
	for _, m := range messages {
		switch m.Role {
		case "assistant":
			if m.Content != "" {
				out = append(out, responses.ResponseInputItemParamOfMessage(m.Content, responses.EasyInputMessageRoleAssistant))
			}
			for _, c := range m.ToolCalls {
				out = append(out, responses.ResponseInputItemParamOfFunctionCall(c.Arguments, c.ID, c.Name))
			}
		case "tool":
			id := ""
			if m.ToolCallID != nil {
				id = *m.ToolCallID
			}
			out = append(out, responses.ResponseInputItemParamOfFunctionCallOutput(id, m.Content))
		default:
			role := responses.EasyInputMessageRoleUser
			if m.Role == "system" {
				role = responses.EasyInputMessageRoleSystem
			}
			out = append(out, responses.ResponseInputItemParamOfMessage(m.Content, role))
		}
	}
	return out
}

func schemaMap(v any) map[string]any {
	if v == nil {
		return nil
	}
	if m, ok := v.(map[string]any); ok {
		return m
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return m
}

func toResponsesTools(tools []ToolDefinition) []responses.ToolUnionParam {
	var out []responses.ToolUnionParam
	for _, t := range tools {
		out = append(out, responses.ToolParamOfFunction(t.Name, schemaMap(t.Parameters), false))
	}
	return out
}

func (p *OpenAIProvider) newResponsesParams(system string, messages []LlmMessage, tools []ToolDefinition, sessionID string) responses.ResponseNewParams {
	params := responses.ResponseNewParams{
		Model: shared.ResponsesModel(p.model),
		Input: responses.ResponseNewParamsInputUnion{
			OfInputItemList: toResponsesInput(messages),
		},
	}
	if system != "" {
		params.Instructions = openai.String(system)
	}
	if len(tools) > 0 {
		params.Tools = toResponsesTools(tools)
	}
	if p.isOpencode {
		if id := strings.TrimSpace(sessionID); id != "" {
			params.PromptCacheKey = openai.String(id)
		}
	}
	return params
}

// parseResponsesOutput flattens a Responses object into the chat-shaped
// ToolResponse the agent loop consumes.
func parseResponsesOutput(res *responses.Response) ToolResponse {
	var out ToolResponse
	if res == nil {
		return out
	}
	var text strings.Builder
	for _, item := range res.Output {
		switch item.Type {
		case "message":
			for _, c := range item.Content {
				if c.Type == "output_text" {
					text.WriteString(c.Text)
				}
			}
		case "function_call":
			out.ToolCalls = append(out.ToolCalls, ToolCall{
				ID:        item.CallID,
				Name:      item.Name,
				Arguments: item.Arguments,
			})
		}
	}
	out.Content = text.String()
	return out
}

// responsesUsage maps a Responses usage block onto Usage. Reasoning
// comes from the typed output details; costs and cache flow from the
// gateway extras in the raw body when present.
func responsesUsage(u responses.ResponseUsage, raw string) *Usage {
	out := &Usage{
		PromptTokens:     u.InputTokens,
		CompletionTokens: u.OutputTokens,
		ReasoningTokens:  u.OutputTokensDetails.ReasoningTokens,
		TotalTokens:      u.TotalTokens,
	}
	if out.TotalTokens == 0 {
		out.TotalTokens = out.PromptTokens + out.CompletionTokens
	}
	if cost, list, cr, cw, ok := gatewayCost(raw); ok {
		out.Cost, out.ListCost, out.HasCost = cost, list, true
		if out.CacheReadTokens == 0 {
			out.CacheReadTokens = cr
		}
		if out.CacheWriteTokens == 0 {
			out.CacheWriteTokens = cw
		}
	}
	if *out == (Usage{}) {
		return nil
	}
	return out
}

func (p *OpenAIProvider) completeResponses(ctx context.Context, system string, messages []LlmMessage, tools []ToolDefinition, sessionID string) (ToolResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()
	res, err := p.client.Responses.New(ctx, p.newResponsesParams(system, messages, tools, sessionID), p.headers(sessionID)...)
	if err != nil {
		// One immediate retry for transport blips (EOF/reset before
		// any response): the SDK retries cover most, this catches the
		// stragglers without turning a blip into a failed turn.
		if isTransientTransport(err) {
			if res2, err2 := p.client.Responses.New(ctx, p.newResponsesParams(system, messages, tools, sessionID), p.headers(sessionID)...); err2 == nil {
				res, err = res2, nil
			} else {
				err = err2
			}
		}
		if err != nil {
			return ToolResponse{}, p.mapError("responses", err)
		}
	}
	if res.Error.Message != "" {
		return ToolResponse{}, RequestError(fmt.Sprintf("%s: responses: %s", p.name, res.Error.Message))
	}
	out := parseResponsesOutput(res)
	out.Usage = responsesUsage(res.Usage, res.RawJSON())
	return out, nil
}

type respCallAcc struct {
	id        string
	name      string
	arguments strings.Builder
	index     int64
}

type respStreamState struct {
	text      strings.Builder
	sentText  bool
	accs      map[string]*respCallAcc
	announced map[string]bool
}

func (p *OpenAIProvider) streamResponses(ctx context.Context, system string, messages []LlmMessage, tools []ToolDefinition, sessionID string, tx chan<- Chunk, done <-chan struct{}) error {
	return p.doStreamResponses(ctx, system, messages, tools, sessionID, tx, done, true)
}

func (p *OpenAIProvider) doStreamResponses(ctx context.Context, system string, messages []LlmMessage, tools []ToolDefinition, sessionID string, tx chan<- Chunk, done <-chan struct{}, allowRetry bool) error {
	parent := ctx
	tctx, idle, cancel := watchIdle(ctx)
	defer cancel()
	stream := p.client.Responses.NewStreaming(tctx, p.newResponsesParams(system, messages, tools, sessionID), p.streamOptions(sessionID)...)
	defer stream.Close()

	send := func(c Chunk) error {
		select {
		case tx <- c:
			return nil
		case <-done:
			return errClientGone
		case <-parent.Done():
			return CancelledError()
		}
	}
	st := &respStreamState{accs: map[string]*respCallAcc{}, announced: map[string]bool{}}
	for stream.Next() {
		idle.touch()
		ev := stream.Current()
		switch ev.Type {
		case "response.output_text.delta":
			text := ev.Delta.OfString
			if text == "" {
				continue
			}
			st.text.WriteString(text)
			st.sentText = true
			if err := send(TextChunk(text)); err != nil {
				return CancelledError()
			}
		case "response.reasoning_summary_text.delta":
			// Reasoning models think before they answer. Forward the
			// summary stream so the UI shows progress instead of
			// minutes of silence; it is not answer text, so it must
			// not mark sentText (the completed envelope still carries
			// the real answer below).
			text := ev.Delta.OfString
			if text == "" {
				continue
			}
			if err := send(TextChunk(text)); err != nil {
				return CancelledError()
			}
		case "response.output_item.added":
			if ev.Item.Type != "function_call" {
				continue
			}
			key := ev.Item.ID
			if key == "" {
				key = ev.Item.CallID
			}
			acc := st.accs[key]
			if acc == nil {
				acc = &respCallAcc{index: ev.OutputIndex}
				st.accs[key] = acc
			}
			acc.id = ev.Item.CallID
			acc.name = ev.Item.Name
			if acc.name != "" && !st.announced[key] {
				st.announced[key] = true
				id := acc.id
				if id == "" {
					id = key
				}
				if err := send(Chunk{ToolNotice: &ToolCall{ID: id, Name: acc.name, Arguments: "{}"}}); err != nil {
					return CancelledError()
				}
			}
		case "response.function_call_arguments.delta":
			acc := st.accs[ev.ItemID]
			if acc == nil {
				acc = &respCallAcc{index: ev.OutputIndex}
				st.accs[ev.ItemID] = acc
			}
			acc.arguments.WriteString(ev.Delta.OfString)
		case "response.completed", "response.incomplete":
			if !st.sentText {
				text := parseResponsesOutput(&ev.Response).Content
				if text != "" {
					st.text.WriteString(text)
					if err := send(TextChunk(text)); err != nil {
						return CancelledError()
					}
				}
			}
			if calls := parseResponsesOutput(&ev.Response).ToolCalls; len(calls) > 0 {
				select {
				case tx <- Chunk{ToolCalls: calls}:
				case <-done:
					return CancelledError()
				case <-parent.Done():
					return CancelledError()
				}
			}
			// The completed envelope carries the turn's usage; bill it
			// even when the model made no tool calls.
			if u := responsesUsage(ev.Response.Usage, ev.Response.RawJSON()); u != nil {
				if err := send(Chunk{Usage: u}); err != nil {
					return CancelledError()
				}
			}
			return nil
		case "response.failed":
			msg := ev.Response.Error.Message
			if msg == "" {
				msg = "response failed"
			}
			return RequestError(fmt.Sprintf("%s: responses: %s", p.name, msg))
		case "error":
			msg := ev.Message
			if msg == "" {
				msg = ev.Code
			}
			if msg == "" {
				msg = "response error"
			}
			return RequestError(fmt.Sprintf("%s: responses: %s", p.name, msg))
		}
		select {
		case <-done:
			return CancelledError()
		case <-parent.Done():
			return CancelledError()
		default:
		}
	}
	if err := stream.Err(); err != nil {
		if parent.Err() != nil {
			return CancelledError()
		}
		if idle.Fired() {
			return idleError(p.name, "responses")
		}
		// Transport died before any data arrived: retry once with a
		// fresh stream instead of failing the turn on a gateway blip.
		if allowRetry && isTransientTransport(err) && !st.sentText && st.text.Len() == 0 && len(st.accs) == 0 {
			return p.doStreamResponses(parent, system, messages, tools, sessionID, tx, done, false)
		}
		return p.mapError("responses", err)
	}
	if calls := fallbackRespCalls(st.accs); len(calls) > 0 {
		select {
		case tx <- Chunk{ToolCalls: calls}:
		case <-done:
			return CancelledError()
		case <-parent.Done():
			return CancelledError()
		}
	}
	return nil
}

// fallbackRespCalls handles gateways that end the SSE stream without a
// response.completed event. The output-item events still carry enough to
// reconstruct each call.
func fallbackRespCalls(accs map[string]*respCallAcc) []ToolCall {
	if len(accs) == 0 {
		return nil
	}
	ordered := make([]*respCallAcc, 0, len(accs))
	for _, acc := range accs {
		if acc.name != "" {
			ordered = append(ordered, acc)
		}
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].index < ordered[j].index })
	out := make([]ToolCall, 0, len(ordered))
	for _, acc := range ordered {
		out = append(out, ToolCall{ID: acc.id, Name: acc.name, Arguments: acc.arguments.String()})
	}
	return out
}
