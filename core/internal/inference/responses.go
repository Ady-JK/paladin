// responses.go implements the OpenAI Responses dialect
// used by the OpenCode Go models that do not accept chat completions.

package inference

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/param"
	"github.com/openai/openai-go/responses"
	"github.com/openai/openai-go/shared"
)

// toResponsesInput builds the Responses request's input items; replay
// decides how an assistant message's reasoning goes back (see
// thinkingPolicy). Native reasoning items go ahead of the message and
// calls they belong to.
// responsesVerbosityLow matches the models VS Code asks to keep answers short.
var responsesVerbosityLow = regexp.MustCompile(`(?i)(?:^|/)(?:gpt-5\.1(?:$|[^0-9])|gpt-5-mini|gpt-5\.6|gpt-6)`)

func toResponsesInput(messages []LlmMessage, replay *thinkingPolicy) responses.ResponseInputParam {
	out := make(responses.ResponseInputParam, 0, len(messages))
	var images []LlmMessage
	flushImages := func() {
		for _, m := range images {
			url, err := m.Image.DataURL()
			if err != nil {
				out = append(out, responses.ResponseInputItemParamOfMessage("Image from tool "+m.NameValue()+" unavailable: "+err.Error(), responses.EasyInputMessageRoleUser))
				continue
			}
			part := responses.ResponseInputContentParamOfInputImage(responses.ResponseInputImageDetailAuto)
			part.OfInputImage.ImageURL = openai.String(url)
			out = append(out, responses.ResponseInputItemParamOfMessage(responses.ResponseInputMessageContentListParam{
				responses.ResponseInputContentParamOfInputText("Image returned by tool " + m.NameValue() + ":"), part,
			}, responses.EasyInputMessageRoleUser))
		}
		images = nil
	}
	lastUser := lastUserIndex(messages)
	for i, m := range messages {
		if m.Role != "tool" {
			flushImages()
		}
		switch m.Role {
		case "assistant":
			content := m.Content
			if replay != nil && m.Thinking != nil {
				if items := nativeReasoningItems(m.Thinking, replay, i, lastUser); len(items) > 0 {
					out = append(out, items...)
				} else if replay.readable && !replay.native(m.Thinking) {
					content = priorReasoning(m.Thinking, content)
				}
			}
			if content != "" {
				out = append(out, responses.ResponseInputItemParamOfMessage(content, responses.EasyInputMessageRoleAssistant))
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
			if m.Image != nil {
				images = append(images, m)
			}
		default:
			role := responses.EasyInputMessageRoleUser
			if m.Role == "system" {
				role = responses.EasyInputMessageRoleSystem
			}
			if m.Image == nil {
				out = append(out, responses.ResponseInputItemParamOfMessage(m.Content, role))
			} else if url, err := m.Image.DataURL(); err == nil {
				part := responses.ResponseInputContentParamOfInputImage(responses.ResponseInputImageDetailAuto)
				part.OfInputImage.ImageURL = openai.String(url)
				out = append(out, responses.ResponseInputItemParamOfMessage(responses.ResponseInputMessageContentListParam{
					responses.ResponseInputContentParamOfInputText(m.Content), part,
				}, role))
			} else {
				out = append(out, responses.ResponseInputItemParamOfMessage(m.Content+" [image unavailable: "+err.Error()+"]", role))
			}
		}
	}
	flushImages()
	return out
}

// nativeReasoningItems are t's stored reasoning items as request input,
// or nil when they cannot go back to this model.
func nativeReasoningItems(t *Thinking, replay *thinkingPolicy, i, lastUser int) []responses.ResponseInputItemUnionParam {
	if !replay.replayNative(t, i, lastUser) {
		return nil
	}
	var raws []json.RawMessage
	if json.Unmarshal(t.Raw, &raws) != nil {
		return nil
	}
	items := make([]responses.ResponseInputItemUnionParam, 0, len(raws))
	for _, raw := range raws {
		items = append(items, param.Override[responses.ResponseInputItemUnionParam](raw))
	}
	return items
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
		Model: p.model,
		Input: responses.ResponseNewParamsInputUnion{
			OfInputItemList: toResponsesInput(messages, p.thinkingReplay()),
		},
	}
	if system != "" {
		if p.isCopilot {
			// VS Code sends the system prompt as the first input message.
			sysItem := responses.ResponseInputItemParamOfMessage(responses.ResponseInputMessageContentListParam{
				responses.ResponseInputContentParamOfInputText(system),
			}, responses.EasyInputMessageRoleSystem)
			params.Input.OfInputItemList = append(responses.ResponseInputParam{sysItem}, params.Input.OfInputItemList...)
		} else {
			params.Instructions = openai.String(system)
		}
	}
	if len(tools) > 0 {
		params.Tools = toResponsesTools(tools)
	}
	effort := p.effort
	if effort == "" && p.isCopilot && len(p.meta.ThinkingLevels) > 0 {
		// A model that declares reasoning levels always gets one: medium.
		effort = EffortMedium
	}
	if effort != "" {
		params.Reasoning = shared.ReasoningParam{Effort: shared.ReasoningEffort(effort), Summary: shared.ReasoningSummaryAuto}
	}
	if p.isOpencode {
		if id := strings.TrimSpace(sessionID); id != "" {
			params.PromptCacheKey = openai.String(id)
		}
	}
	if p.isCopilot {
		// Ask for the encrypted reasoning explicitly (GPT-6 needs it) and
		// keep nothing server-side: the reasoning travels with the history.
		params.Include = []responses.ResponseIncludable{responses.ResponseIncludableReasoningEncryptedContent}
		params.Store = openai.Bool(false)
		params.Truncation = responses.ResponseNewParamsTruncationDisabled
		if p.meta.MaxOutput > 0 {
			params.MaxOutputTokens = openai.Int(p.meta.MaxOutput)
		}
		if responsesVerbosityLow.MatchString(p.model) {
			// The SDK has no typed verbosity field yet.
			params.SetExtraFields(map[string]any{"text": map[string]any{"verbosity": "low"}})
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
		case "reasoning":
			out.Reasoning += reasoningSummary(item)
		}
	}
	out.Content = text.String()
	return out
}

// reasoningSummary is a reasoning item's readable summary.
func reasoningSummary(item responses.ResponseOutputItemUnion) string {
	var parts []string
	for _, s := range item.Summary {
		if s.Text != "" {
			parts = append(parts, s.Text)
		}
	}
	return strings.Join(parts, "\n\n")
}

// reasoningItems is the response's reasoning items as a JSON array, the
// replayable form; nil when it made none.
func reasoningItems(res *responses.Response) json.RawMessage {
	var raws []json.RawMessage
	for _, item := range res.Output {
		if item.Type == "reasoning" {
			raws = append(raws, json.RawMessage(item.RawJSON()))
		}
	}
	if len(raws) == 0 {
		return nil
	}
	raw, err := json.Marshal(raws)
	if err != nil {
		return nil
	}
	return raw
}

// responsesThinking labels a response's reasoning items as p's.
func (p *OpenAIProvider) responsesThinking(raw json.RawMessage) *Thinking {
	if p.isOpencode || len(raw) == 0 {
		return nil
	}
	return p.stampThinking(&Thinking{Format: thinkingResponses, Raw: raw})
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
	captureCacheDetails(out, raw, "input_tokens_details")
	if out.TotalTokens == 0 {
		out.TotalTokens = out.PromptTokens + out.CompletionTokens
	}
	if ge, ok := gatewayCost(raw); ok {
		applyGatewayCost(out, ge.Cost, ge.ListCost, ge.HasCost, ge.HasList)
		if out.CacheReadTokens == 0 {
			out.CacheReadTokens = ge.CacheRead
		}
		if out.CacheWriteTokens == 0 {
			out.CacheWriteTokens = ge.CacheWrite
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
	var httpRes *http.Response
	opts := append(p.headers(sessionID), option.WithResponseInto(&httpRes))
	opts = append(opts, p.copilotOptions(ctx, messages)...)
	res, err := p.client.Responses.New(ctx, p.newResponsesParams(system, messages, tools, sessionID), opts...)
	if err != nil {
		// One immediate retry for transport blips (EOF/reset before
		// any response): the SDK retries cover most, this catches the
		// stragglers without turning a blip into a failed turn.
		if isTransientTransport(err) {
			if res2, err2 := p.client.Responses.New(ctx, p.newResponsesParams(system, messages, tools, sessionID), opts...); err2 == nil {
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
	out.Thinking = p.responsesThinking(reasoningItems(res))
	out.Usage = withHeaderCost(responsesUsage(res.Usage, res.RawJSON()), httpRes)
	p.estimateCopilotUsage(out.Usage)
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
	// reasoning holds reasoning items as they finish, for streams that
	// end without a completed envelope.
	reasoning []json.RawMessage
}

func (p *OpenAIProvider) streamResponses(ctx context.Context, system string, messages []LlmMessage, tools []ToolDefinition, sessionID string, tx chan<- Chunk, done <-chan struct{}) error {
	return p.doStreamResponses(ctx, system, messages, tools, sessionID, tx, done, true)
}

func (p *OpenAIProvider) doStreamResponses(ctx context.Context, system string, messages []LlmMessage, tools []ToolDefinition, sessionID string, tx chan<- Chunk, done <-chan struct{}, allowRetry bool) error {
	parent := ctx
	tctx, idle, cancel := watchIdle(ctx)
	defer cancel()
	opts := append(p.streamOptions(sessionID), p.copilotOptions(ctx, messages)...)
	stream := p.client.Responses.NewStreaming(tctx, p.newResponsesParams(system, messages, tools, sessionID), opts...)
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
			// summary stream as reasoning so the UI shows progress
			// instead of minutes of silence; it is not answer text, so
			// it must not mark sentText (the completed envelope still
			// carries the real answer below).
			text := ev.Delta.OfString
			if text == "" {
				continue
			}
			if err := send(Chunk{Reasoning: text}); err != nil {
				return CancelledError()
			}
		case "response.output_item.done":
			if ev.Item.Type == "reasoning" {
				st.reasoning = append(st.reasoning, json.RawMessage(ev.Item.RawJSON()))
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
			if items := reasoningItems(&ev.Response); items != nil {
				if err := send(Chunk{Thinking: p.responsesThinking(items)}); err != nil {
					return CancelledError()
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
				p.estimateCopilotUsage(u)
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
	if len(st.reasoning) > 0 {
		if raw, err := json.Marshal(st.reasoning); err == nil {
			if err := send(Chunk{Thinking: p.responsesThinking(raw)}); err != nil {
				return CancelledError()
			}
		}
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
