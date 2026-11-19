package runtime

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/Ady-JK/paladin/core/internal/agent"
	"github.com/Ady-JK/paladin/core/internal/atomicfile"
	"github.com/Ady-JK/paladin/core/internal/codex"
	"github.com/Ady-JK/paladin/core/internal/jsonx"
	"github.com/Ady-JK/paladin/core/internal/session"
	"github.com/Ady-JK/paladin/core/internal/tools"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

// planUpdated shows Codex's step list as Paladin's todo list: the same panel
// above the composer, kept in the session's todos.json like the todo tool's.
func (c *codexRun) planUpdated(p codex.Raw) {
	steps := p.List("plan")
	todos := make([]tools.TodoItem, 0, len(steps))
	for i, step := range steps {
		content := strings.TrimSpace(step.Str("step"))
		if content == "" {
			content = fmt.Sprintf("Step %d", i+1)
		}
		status := tools.TodoNotStarted
		switch step.Str("status") {
		case "inProgress":
			status = tools.TodoInProgress
		case "completed":
			status = tools.TodoFinished
		}
		todos = append(todos, tools.TodoItem{Content: content, Status: status})
	}
	if len(todos) == 0 {
		return
	}
	// Paladin's list allows one step in progress; keep the first.
	seen := false
	for i := range todos {
		if todos[i].Status == tools.TodoInProgress {
			if seen {
				todos[i].Status = tools.TodoNotStarted
			}
			seen = true
		}
	}
	data, err := jsonx.Marshal(map[string]any{"todos": todos})
	if err != nil {
		return
	}
	if c.dir != "" {
		_ = atomicfile.Write(filepath.Join(c.dir, session.TodoFileName), data)
	}
	id := "plan-" + c.turnID
	args := string(data)
	// The same card id each update replaces the one before it.
	c.send(agent.ChatChunk{Tool: &agent.ToolActivity{ID: id, Name: "todo", Arguments: args, Result: args, Status: session.ToolOK}})
	if c.planRow < 0 {
		c.flushText([]transcript.ToolCall{{ID: id, Name: "todo", Arguments: args}})
		callID, name := id, "todo"
		c.records = append(c.records, transcript.Message{Role: transcript.RoleTool, Content: args,
			CreatedAt: time.Now().UTC(), ToolCallID: &callID, Name: &name})
		c.planRow = len(c.records) - 1
		return
	}
	// A later update rewrites the list in the rows already recorded.
	c.records[c.planRow].Content = args
	if calls := c.records[c.planRow-1].ToolCalls; calls != nil && len(*calls) > 0 {
		(*calls)[0].Arguments = args
	}
}

// askQuestions answers Codex's structured questions (request_user_input) with
// Paladin's question wizard: the questions arrive as an `ask` tool call, the
// user's answers go back to Codex.
func (c *codexRun) askQuestions(m codex.Inbound, p codex.Raw) error {
	callID := p.Str("itemId")
	if callID == "" {
		callID = string(m.RequestID)
	}
	var asks []tools.AskQuestion
	var ids []string
	for _, q := range p.List("questions") {
		text := strings.TrimSpace(q.Str("question"))
		if h := strings.TrimSpace(q.Str("header")); h != "" && h != text {
			text = h + ": " + text
		}
		var options []string
		for _, o := range q.List("options") {
			if label := strings.TrimSpace(o.Str("label")); label != "" && len(options) < tools.MaxAskOptions {
				options = append(options, label)
			}
		}
		asks = append(asks, tools.AskQuestion{Question: text, Options: options})
		ids = append(ids, q.Str("id"))
	}
	if len(asks) == 0 {
		return c.client.RespondError(m.RequestID, -32602, "no questions")
	}
	if len(asks) > tools.MaxAskQuestions {
		asks, ids = asks[:tools.MaxAskQuestions], ids[:tools.MaxAskQuestions]
	}
	answers, err := c.ask(callID, asks)
	if err != nil {
		// Dismissed or interrupted: the turn is being stopped by whoever did it.
		_ = c.client.RespondError(m.RequestID, -32000, "the user dismissed the question")
		return nil //nolint:nilerr
	}
	given := map[string]any{}
	for i, a := range answers {
		if i >= len(ids) {
			break
		}
		values := a.Selected
		if len(values) == 0 {
			values = []string{a.Answer}
		}
		given[ids[i]] = map[string]any{"answers": values}
	}
	return c.client.Respond(m.RequestID, map[string]any{"answers": given})
}

// ask shows questions in Paladin's wizard as an `ask` card and waits for the
// user's answers, recording the card in the turn like any other tool call. An
// error means the questions were dismissed or the turn was stopped.
func (c *codexRun) ask(callID string, asks []tools.AskQuestion) ([]tools.AskAnswer, error) {
	args, _ := json.Marshal(map[string]any{"questions": asks})
	t := &codexTool{id: callID, name: "ask", args: string(args)}
	c.tools[callID] = t
	c.send(agent.ChatChunk{Tool: &agent.ToolActivity{ID: callID, Name: "ask", Arguments: t.args, Status: session.ToolRunning}})
	result, err := c.r.ask.Call(args, &tools.ToolCtx{SessionID: c.sessionID, CallID: callID, Cancel: c.ctx})
	if err != nil {
		c.finishTool(t, mustJSON(map[string]any{"error": err.Error()}), true)
		return nil, err
	}
	var reply struct {
		Answers []tools.AskAnswer `json:"answers"`
	}
	_ = json.Unmarshal([]byte(result), &reply)
	c.finishTool(t, result, false)
	return reply.Answers, nil
}
