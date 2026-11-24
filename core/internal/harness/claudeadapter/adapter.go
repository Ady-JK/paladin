// Package claudeadapter translates Claude Code events into Paladin's shared
// cards and transcript. It has no process, filesystem, or UI dependencies.
package claudeadapter

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Ady-JK/paladin/core/internal/agent"
	"github.com/Ady-JK/paladin/core/internal/claude"
	"github.com/Ady-JK/paladin/core/internal/harness"
	"github.com/Ady-JK/paladin/core/internal/session"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

const Name = "claude"

type Adapter struct{}

func New() Adapter                                                      { return Adapter{} }
func (Adapter) Name() string                                            { return Name }
func (Adapter) Annotate(msgs []transcript.Message) []transcript.Message { return msgs }
func (Adapter) QuestionReply(_ string, _ []harness.Question, answers []harness.Answer) (string, string) {
	b, _ := json.Marshal(answers)
	return "Answers to your questions: " + string(b), "Answered Claude's questions"
}
func encode(v any) string { b, _ := json.Marshal(v); return string(b) }

// Tool maps native inputs to the names/arguments of Paladin's existing cards.
func Tool(id, name string, input claude.Raw) harness.Card {
	args := string(mustRaw(input))
	kind := name
	switch name {
	case "Bash", "PowerShell":
		kind = "shell"
		args = encode(map[string]any{"command": input.Str("command"), "reason": input.Str("description"), "background": input.Bool("run_in_background")})
	case "Read":
		kind = "read"
		args = encode(map[string]any{"path": input.Str("file_path"), "offset": input.Int("offset"), "limit": input.Int("limit")})
	case "Write":
		kind = "write"
		args = encode(map[string]any{"path": input.Str("file_path"), "content": input.Str("content")})
	case "Edit":
		kind = "edit"
		args = encode(map[string]any{"path": input.Str("file_path"), "old_string": input.Str("old_string"), "new_string": input.Str("new_string"), "replace_all": input.Bool("replace_all")})
	case "Glob":
		kind = "glob"
	case "Grep":
		kind = "grep"
	case "WebFetch":
		kind = "web_fetch"
	case "WebSearch":
		kind = "web_search"
	case "AskUserQuestion":
		kind = "ask"
	case "TodoWrite":
		kind = "todo"
		items := []map[string]any{}
		for _, todo := range input.List("todos") {
			status := "not_started"
			switch todo.Str("status") {
			case "in_progress":
				status = "in_progress"
			case "completed":
				status = "finished"
			}
			items = append(items, map[string]any{"content": todo.Str("content"), "status": status})
		}
		args = encode(map[string]any{"todos": items})
	default:
		if !strings.HasPrefix(name, "mcp__") {
			kind = "claude_" + name
		}
	}
	return harness.Card{ID: id, Name: kind, Args: args}
}
func mustRaw(r claude.Raw) []byte { b, _ := json.Marshal(r); return b }

// Projection owns only the state needed to reconcile partial API events with
// complete assistant messages. Complete messages may also arrive without deltas.
type Projection struct {
	Records        []transcript.Message
	Reply          strings.Builder
	Tools          map[string]harness.Card
	NativeTools    map[string]string
	Seen           map[string]bool
	partial        map[string]int
	messageID      string
	blocks         map[int64]claude.Raw
	text, thinking strings.Builder
}

func NewProjection() *Projection {
	return &Projection{Tools: map[string]harness.Card{}, NativeTools: map[string]string{}, Seen: map[string]bool{}, partial: map[string]int{}, blocks: map[int64]claude.Raw{}}
}
func (p *Projection) Flush(calls []transcript.ToolCall) {
	if p.text.Len() == 0 && p.thinking.Len() == 0 && len(calls) == 0 {
		return
	}
	m := transcript.Message{Role: transcript.RoleAssistant, Content: p.text.String(), Reasoning: p.thinking.String(), CreatedAt: time.Now().UTC()}
	if len(calls) > 0 {
		m.ToolCalls = &calls
	}
	p.Records = append(p.Records, m)
	p.text.Reset()
	p.thinking.Reset()
}

// Snapshot captures partial prose and calls about to execute without flushing
// them out of the live projection. Recovery can then show interrupted work.
func (p *Projection) Snapshot() []transcript.Message {
	rows := append([]transcript.Message{}, p.Records...)
	var calls []transcript.ToolCall
	for _, card := range p.Tools {
		calls = append(calls, transcript.ToolCall{ID: card.ID, Name: card.Name, Arguments: card.Args})
	}
	if p.text.Len() > 0 || p.thinking.Len() > 0 || len(calls) > 0 {
		m := transcript.Message{Role: transcript.RoleAssistant, Content: p.text.String(), Reasoning: p.thinking.String(), CreatedAt: time.Now().UTC()}
		if len(calls) > 0 {
			m.ToolCalls = &calls
		}
		rows = append(rows, m)
	}
	return rows
}

func (p *Projection) textDelta(key, text string, thinking bool) agent.ChatChunk {
	p.partial[key] += len(text)
	if thinking {
		p.thinking.WriteString(text)
		return agent.ChatChunk{Reasoning: text}
	}
	p.text.WriteString(text)
	p.Reply.WriteString(text)
	return agent.ChatChunk{Delta: text}
}
func (p *Projection) CompleteTool(id, result string, failed bool) *agent.ToolActivity {
	card, ok := p.Tools[id]
	if !ok {
		return nil
	}
	status := session.ToolOK
	if failed {
		status = session.ToolFailed
	}
	p.Flush([]transcript.ToolCall{{ID: id, Name: card.Name, Arguments: card.Args}})
	name := card.Name
	p.Records = append(p.Records, transcript.Message{Role: transcript.RoleTool, Content: result, CreatedAt: time.Now().UTC(), Name: &name, ToolCallID: &id, IsError: failed})
	delete(p.Tools, id)
	return &agent.ToolActivity{ID: id, Name: card.Name, Arguments: card.Args, Result: result, Status: status}
}
func (p *Projection) Apply(r claude.Raw) []agent.ChatChunk {
	out := []agent.ChatChunk{}
	// Child streams are separate conversations: show their calls with distinct
	// IDs, but never append their prose to the main answer.
	child := r.Str("parent_tool_use_id")
	switch r.Str("type") {
	case "stream_event":
		if child != "" {
			return out
		}
		e := r.Obj("event")
		switch e.Str("type") {
		case "message_start":
			p.messageID = e.Obj("message").Str("id")
			p.blocks = map[int64]claude.Raw{}
		case "content_block_start":
			p.blocks[e.Int("index")] = e.Obj("content_block")
		case "content_block_delta":
			d := e.Obj("delta")
			key := fmt.Sprintf("%s:%d", p.messageID, e.Int("index"))
			switch d.Str("type") {
			case "text_delta":
				out = append(out, p.textDelta(key, d.Str("text"), false))
			case "thinking_delta":
				out = append(out, p.textDelta(key, d.Str("thinking"), true))
			}
		}
	case "assistant":
		m := r.Obj("message")
		id := m.Str("id")
		identity := r.Str("uuid")
		if identity == "" {
			identity = id + ":" + m.JSON("content")
		}
		if p.Seen[identity] {
			return out
		}
		p.Seen[identity] = true
		if child == "" && p.text.Len() > 0 && p.messageID != id {
			out = append(out, p.textDelta(id+":separator", "\n\n", false))
		}
		content := m.List("content")
		for i, b := range content {
			index := int64(i)
			if _, present := r["apiBlockIndex"]; present {
				index += r.Int("apiBlockIndex")
			} else if len(content) == 1 && p.messageID == id {
				for j, block := range p.blocks {
					if block.Str("type") == b.Str("type") && j > index {
						index = j
					}
				}
			}
			key := fmt.Sprintf("%s:%d", id, index)
			switch b.Str("type") {
			case "text", "thinking":
				if child != "" {
					continue
				}
				text := b.Str(b.Str("type"))
				shown := p.partial[key]
				if shown < len(text) {
					out = append(out, p.textDelta(key, text[shown:], b.Str("type") == "thinking"))
				}
			case "tool_use", "server_tool_use":
				tid := b.Str("id")
				if _, exists := p.Tools[tid]; exists {
					continue
				}
				card := Tool(tid, b.Str("name"), b.Obj("input"))
				p.Tools[tid] = card
				p.NativeTools[tid] = b.Str("name")
				out = append(out, agent.ChatChunk{Tool: &agent.ToolActivity{ID: tid, Name: card.Name, Arguments: card.Args, Status: session.ToolRunning}})
			}
		}
	case "user":
		for _, b := range r.Obj("message").List("content") {
			if b.Str("type") != "tool_result" {
				continue
			}
			id := b.Str("tool_use_id")
			text := b.Str("content")
			if text == "" {
				var parts []string
				for _, c := range b.List("content") {
					if c.Str("type") == "text" {
						parts = append(parts, c.Str("text"))
					}
				}
				text = strings.Join(parts, "\n")
			}
			result := encode(map[string]any{"output": text})
			if b.Bool("is_error") {
				result = encode(map[string]any{"error": text})
			}
			if tool := p.CompleteTool(id, result, b.Bool("is_error")); tool != nil {
				out = append(out, agent.ChatChunk{Tool: tool})
			}
		}
	case "system":
		switch r.Str("subtype") {
		case "compact_boundary":
			p.Flush(nil)
			p.Records = append(p.Records, transcript.Message{Role: transcript.RoleSystem, Content: "Context compacted", Compaction: &transcript.Compaction{Reason: "auto"}, CreatedAt: time.Now().UTC()})
			out = append(out, agent.ChatChunk{Compaction: &agent.CompactionStatus{Phase: "done", Reason: "auto"}})
		case "api_retry":
			out = append(out, agent.ChatChunk{Retry: &agent.RetryStatus{Attempt: int(r.Int("attempt")), Max: int(r.Int("max_retries")), DelayMs: r.Int("retry_delay_ms"), Reason: r.Str("error")}})
		// task_* events are not notices: a background task already has its own
		// claude_task card, and echoing its command here repeated it.
		case "hook_response", "permission_denied":
			text := r.Str("description")
			if text == "" {
				text = r.Str("summary")
			}
			if text == "" {
				text = r.Str("output")
			}
			if text != "" {
				out = append(out, agent.ChatChunk{Notice: &agent.ChatNotice{Kind: "info", Text: text}})
			}
		}
	}
	return out
}
