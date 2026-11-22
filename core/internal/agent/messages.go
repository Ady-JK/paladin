// messages.go shapes the on-disk transcript into the
// model-facing message list and renders background-job notices.

package agent

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/Ady-JK/paladin/core/internal/inference"
	"github.com/Ady-JK/paladin/core/internal/media"
	"github.com/Ady-JK/paladin/core/internal/session"
	"github.com/Ady-JK/paladin/core/internal/tools"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

// toTranscriptThinking and fromTranscriptThinking convert a round's
// replayable reasoning to and from its on-disk form.
func toTranscriptThinking(t *inference.Thinking) *transcript.Thinking {
	if t == nil {
		return nil
	}
	return &transcript.Thinking{Text: t.Text, Provider: t.Provider, Model: t.Model, Format: t.Format, Opaque: t.Opaque, Raw: t.Raw}
}

func fromTranscriptThinking(t *transcript.Thinking) *inference.Thinking {
	if t == nil {
		return nil
	}
	return &inference.Thinking{Text: t.Text, Provider: t.Provider, Model: t.Model, Format: t.Format, Opaque: t.Opaque, Raw: t.Raw}
}

func (a *Agent) modelMessages(message string, resume bool) []inference.LlmMessage {
	withTools := len(a.toolDefs) > 0
	messages := LlmMessages(a.history, withTools, a.imagesAccepted())
	if withTools && !resume {
		messages = append(messages, inference.LlmMessage{Role: "user", Content: message})
		for _, attachment := range a.currentAttachments {
			if attachment.Image != nil {
				messages = append(messages, a.imageMessage("Attached image: "+attachment.Name, attachment.Image))
			}
		}
	}
	return messages
}

// imagesAccepted caches the active model's image capability for the
// turn; an image-blind model keeps working after a switch mid-session
// because its calls carry no image parts at all.
func (a *Agent) imagesAccepted() bool {
	if a.provider == nil {
		return true
	}
	if a.imageOK == nil {
		ok := inference.ModelAcceptsImages(a.provider)
		a.imageOK = &ok
	}
	return *a.imageOK
}

// imageMessage renders one image for the active model: the image itself
// for a model that accepts them, a short note for one that does not, so
// the tool result or attachment still reads as part of the exchange.
func (a *Agent) imageMessage(label string, img *media.Image) inference.LlmMessage {
	if a.imagesAccepted() {
		return inference.LlmMessage{Role: "user", Content: label, Image: img}
	}
	name := ""
	if img != nil {
		name = filepath.Base(img.Path)
	}
	return inference.LlmMessage{Role: "user", Content: label + " (" + imageBlindNote(name) + ")"}
}

// AbortedToolContent is the model-facing result for a tool call that
// never produced one (stop mid-tools, crash, interrupted persist).
// Providers reject an assistant tool call without a matching result,
// so history repair answers it with this instead of failing the turn.
const AbortedToolContent = `{"error":"aborted: this tool call did not complete (the turn was stopped or interrupted); its result is unknown"}`

// LlmMessages builds the LLM view of the stored history. Tool traffic
// only travels with tool-capable providers. Every assistant tool call is
// answered by exactly one tool message directly after it: missing
// answers are synthesized as aborted, duplicates and stray results
// (reminders, orphans, empty or unknown ids) become plain user context
// after the block instead of breaking the provider contract. Only the
// part after the last compaction marker is replayed, behind its summary.
func LlmMessages(history []transcript.Message, withTools, imagesAccepted bool) []inference.LlmMessage {
	builder := messageBuilder{out: []inference.LlmMessage{}, withTools: withTools, imagesAccepted: imagesAccepted}
	// The latest compaction summary replaces all earlier messages.
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].IsCompaction() {
			builder.out = append(builder.out, inference.LlmMessage{Role: "user", Content: compactionUserContent(history[i].Content)})
			history = history[i+1:]
			break
		}
	}
	for _, msg := range history {
		switch msg.Role {
		case transcript.RoleUser:
			builder.addUser(msg)
		case transcript.RoleAssistant:
			builder.addAssistant(msg)
		case transcript.RoleTool:
			builder.addTool(msg)
		case transcript.RoleSystem:
		}
	}
	builder.closeBlock()
	return builder.out
}

type messageBuilder struct {
	out       []inference.LlmMessage
	withTools bool
	// imagesAccepted is the active model's image capability; a blind
	// model gets a text note instead of the image part a text-only
	// endpoint would reject outright.
	imagesAccepted bool
	open           bool
	order          []inference.ToolCall
	answered       map[string]bool
	deferred       []inference.LlmMessage
}

func (b *messageBuilder) closeBlock() {
	if !b.open {
		return
	}
	for _, call := range b.order {
		if b.answered[call.ID] {
			continue
		}
		id, name := call.ID, call.Name
		b.out = append(b.out, inference.LlmMessage{Role: "tool", Content: AbortedToolContent, ToolCallID: &id, Name: &name})
	}
	b.out = append(b.out, b.deferred...)
	b.open, b.order, b.answered, b.deferred = false, nil, nil, nil
}

func (b *messageBuilder) addUser(msg transcript.Message) {
	b.closeBlock()
	b.out = append(b.out, inference.LlmMessage{Role: "user", Content: msg.Content})
	for _, attachment := range msg.Attachments {
		if attachment.Image != nil {
			if b.imagesAccepted {
				b.out = append(b.out, inference.LlmMessage{Role: "user", Content: "Attached image: " + attachment.Name, Image: attachment.Image})
			} else {
				b.out = append(b.out, inference.LlmMessage{Role: "user", Content: "Attached image: " + attachment.Name + " (" + imageBlindNote(attachment.Name) + ")"})
			}
		}
	}
}

func (b *messageBuilder) addAssistant(msg transcript.Message) {
	b.closeBlock()
	var calls []inference.ToolCall
	seen := map[string]bool{}
	if b.withTools && msg.ToolCalls != nil {
		for _, call := range *msg.ToolCalls {
			if _, duplicate := seen[call.ID]; call.ID == "" || duplicate {
				continue
			}
			seen[call.ID] = false
			calls = append(calls, inference.ToolCall{ID: call.ID, Name: call.Name, Arguments: call.Arguments, Signature: call.Signature})
		}
	}
	if strings.TrimSpace(msg.Content) == "" && len(calls) == 0 {
		return
	}
	b.out = append(b.out, inference.LlmMessage{Role: "assistant", Content: msg.Content, ToolCalls: calls, Thinking: fromTranscriptThinking(msg.Thinking)})
	if len(calls) > 0 {
		b.open, b.order, b.answered = true, calls, seen
	}
}

func (b *messageBuilder) addTool(msg transcript.Message) {
	if !b.withTools {
		return
	}
	id := ""
	if msg.ToolCallID != nil {
		id = *msg.ToolCallID
	}
	wasAnswered, known := b.answered[id]
	if !b.open || id == "" || session.IsNotifyMessage(msg) || wasAnswered || !known {
		b.demote(msg.Content)
		return
	}
	b.answered[id] = true
	callID := id
	image := msg.Image
	if image != nil && !b.imagesAccepted {
		// A text-only model must not receive the image part its endpoint
		// would reject; the note keeps the tool result honest.
		name := "an image"
		if msg.Name != nil && *msg.Name != "" {
			name = *msg.Name
		}
		b.out = append(b.out, inference.LlmMessage{
			Role: "tool", Content: msg.Content + "\n[" + imageBlindNote(name) + "]",
			Image: nil, ToolCallID: &callID, Name: msg.Name,
		})
		return
	}
	b.out = append(b.out, inference.LlmMessage{
		Role: "tool", Content: msg.Content,
		Image: image, ToolCallID: &callID, Name: msg.Name,
	})
}

// imageBlindNote is what replaces an image for a model with no image
// inputs: honest about the missing picture, short enough not to crowd
// the exchange.
func imageBlindNote(name string) string {
	if name == "" {
		return "this model does not accept images, so an image is not shown"
	}
	return "this model does not accept images, so the image from " + name + " is not shown"
}

func (b *messageBuilder) demote(content string) {
	msg := inference.LlmMessage{Role: "user", Content: content}
	if b.open {
		b.deferred = append(b.deferred, msg)
		return
	}
	b.out = append(b.out, msg)
}

func blockHas(calls []inference.ToolCall, id string) bool {
	for _, c := range calls {
		if c.ID == id {
			return true
		}
	}
	return false
}

// notifyContent renders a finished background job as the model-facing
// reminder payload.
func notifyContent(notice *tools.NotifyResult) string {
	out := map[string]any{
		"notice":    "background job finished",
		"job_id":    notice.JobID,
		"status":    notice.Status,
		"exit_code": notice.ExitCode,
		"output":    notice.Output,
		"truncated": notice.Truncated,
	}
	if notice.KilledByUser {
		out["notice"] = "the user stopped this background job"
		out["killed_by_user"] = true
	}
	data, err := json.Marshal(out)
	if err != nil {
		return "{}"
	}
	return string(data)
}
