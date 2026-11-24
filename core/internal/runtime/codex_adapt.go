package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/Ady-JK/paladin/core/internal/agent"
	"github.com/Ady-JK/paladin/core/internal/codex"
	"github.com/Ady-JK/paladin/core/internal/harness"
	"github.com/Ady-JK/paladin/core/internal/harness/claudeadapter"
	"github.com/Ady-JK/paladin/core/internal/harness/codexadapter"
	"github.com/Ady-JK/paladin/core/internal/session"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

// The translation from Codex's items to Paladin's cards lives in the codexadapter
// package; this file is the runtime's side of it: turning a card into a live
// event and a transcript row, and following what outlives a turn.

// adapters are the harness adapters by the name a session stores.
var adapters = map[string]harness.Adapter{codexadapter.Name: codexadapter.New(), claudeadapter.Name: claudeadapter.New()}

// adapterFor returns the adapter of a session's harness, nil for a Paladin session.
func adapterFor(name string) harness.Adapter { return adapters[name] }

// toolFor maps a Codex item onto a Paladin tool card.
func (c *codexRun) toolFor(item codex.Raw) *codexTool {
	card := codexadapter.Card(item)
	if card == nil {
		return nil
	}
	return &codexTool{id: card.ID, name: card.Name, args: card.Args}
}

// questionAsked shows a question Codex asked without stopping its work as an
// inline card. It is pending until the user's reply names it.
func (c *codexRun) questionAsked(card *harness.Card) {
	c.finishTool(&codexTool{id: card.ID, name: card.Name, args: card.Args}, card.Result, false)
	if c.questions == nil {
		c.questions = map[string]bool{}
	}
	c.questions[card.ID] = true
}

// settleQuestions marks the cards of this turn that a reply the user just sent
// answers. Cards of earlier turns settle when the history is read (Annotate).
func (c *codexRun) settleQuestions(message string) {
	replies, ok := codexadapter.ParseReply(message)
	if !ok {
		return
	}
	byCard := map[string][]codexadapter.Reply{}
	for _, r := range replies {
		if call, _, ok := codexadapter.ParseQuestionID(r.QuestionItemID); ok && c.questions[call] {
			byCard[call] = append(byCard[call], r)
		}
	}
	for call, rs := range byCard {
		result := codexadapter.AnsweredResult(rs)
		for i := range c.records {
			rec := &c.records[i]
			if rec.Role != transcript.RoleTool || rec.ToolCallID == nil || *rec.ToolCallID != call {
				continue
			}
			rec.Content = result
			c.send(agent.ChatChunk{Tool: &agent.ToolActivity{ID: call, Name: harness.CardQuestion, Arguments: c.questionArgs(call), Result: result, Status: session.ToolOK}})
		}
	}
}

// questionArgs finds the arguments a question card was recorded with.
func (c *codexRun) questionArgs(call string) string {
	for _, rec := range c.records {
		if rec.ToolCalls == nil {
			continue
		}
		for _, tc := range *rec.ToolCalls {
			if tc.ID == call {
				return tc.Arguments
			}
		}
	}
	return "{}"
}

// bgCommand is a command Codex runs on a process of its own, which may outlive the
// turn that started it.
type bgCommand struct {
	processID string
	output    strings.Builder
	done      bool
	result    string
	failed    bool
}

// bgState is a thread's record of its running commands.
type bgState struct {
	mu   sync.Mutex
	cmds map[string]*bgCommand
}

// watchCommand starts following a command that has a process: if its turn ends
// before it does, this is how its real outcome finds its card.
func (th *codexThread) watchCommand(id, processID string) {
	if th == nil || id == "" {
		return
	}
	th.bg.mu.Lock()
	defer th.bg.mu.Unlock()
	if th.bg.cmds == nil {
		th.bg.cmds = map[string]*bgCommand{}
	}
	th.bg.cmds[id] = &bgCommand{processID: processID}
}

// forgetCommand stops following a command that ended inside its own turn.
func (th *codexThread) forgetCommand(id string) {
	if th == nil {
		return
	}
	th.bg.mu.Lock()
	defer th.bg.mu.Unlock()
	delete(th.bg.cmds, id)
}

// noteLate applies a message about an earlier turn: a followed command's output, and
// its completion, which settles the card the turn left running.
func (th *codexThread) noteLate(method string, p codex.Raw) {
	if th == nil {
		return
	}
	switch method {
	case "item/commandExecution/outputDelta":
		th.bg.mu.Lock()
		if cmd := th.bg.cmds[p.Str("itemId")]; cmd != nil && cmd.output.Len() < codexToolOutputMax {
			cmd.output.WriteString(p.Str("delta"))
		}
		th.bg.mu.Unlock()
	case "item/completed":
		item := p.Obj("item")
		if !codexadapter.Settled(item) {
			return
		}
		th.bg.mu.Lock()
		cmd := th.bg.cmds[item.Str("id")]
		if cmd != nil && !cmd.done {
			cmd.result, cmd.failed = codexadapter.Outcome(item, cmd.output.String())
			cmd.done = true
		}
		th.bg.mu.Unlock()
		if cmd != nil {
			th.flushBackground()
		}
	}
}

// flushBackground writes the outcome of every followed command that has ended into
// its card. A card whose turn has not been saved yet is tried again after the save.
func (th *codexThread) flushBackground() {
	if th == nil {
		return
	}
	type ready struct {
		id     string
		result string
		failed bool
	}
	var ended []ready
	th.bg.mu.Lock()
	for id, cmd := range th.bg.cmds {
		if cmd.done {
			ended = append(ended, ready{id, cmd.result, cmd.failed})
		}
	}
	th.bg.mu.Unlock()
	settled := false
	for _, e := range ended {
		n, err := th.r.patchToolRow(th.sessionID, th.storeCwd, e.id, e.result, e.failed)
		if err == nil && n == 0 {
			continue // its turn is not saved yet
		}
		th.forgetCommand(e.id)
		settled = settled || n > 0
	}
	if settled {
		th.r.hub.PublishSession(th.sessionID, "session/refresh")
	}
}

// patchToolRow replaces the stored result of one tool call, under the session's
// persistence gate. It returns how many rows changed.
func (r *AgentRuntime) patchToolRow(sessionID, cwd, callID, result string, failed bool) (int, error) {
	sess, err := r.transcripts.Load(cwd, sessionID)
	if err != nil {
		return 0, err
	}
	n := 0
	err = r.hub.Gated(sessionID, func() error {
		var rerr error
		n, rerr = sess.RewriteMessages(func(m *transcript.Message) bool {
			if m.Role != transcript.RoleTool || m.ToolCallID == nil || *m.ToolCallID != callID || (m.Content == result && m.IsError == failed) {
				return false
			}
			m.Content, m.IsError = result, failed
			return true
		})
		return rerr
	})
	return n, err
}

func mustJSON(v any) string { return codexadapter.JSON(v) }

// AnswerQuestion sends the user's answers to a question an agent asked without
// stopping its work. The adapter writes the reply in the form its agent reads; a
// turn that is running takes it in at once, and otherwise it begins a turn.
func (r *AgentRuntime) AnswerQuestion(req agent.Request, callID string, answers []harness.Answer) (TurnInfo, error) {
	if strings.TrimSpace(req.SessionID) == "" || strings.TrimSpace(callID) == "" {
		return TurnInfo{}, errors.New("session id and question id are required")
	}
	if len(answers) == 0 {
		return TurnInfo{}, errors.New("answer at least one question")
	}
	sess, err := r.transcripts.Load(req.Cwd, req.SessionID)
	if err != nil {
		return TurnInfo{}, err
	}
	if sess.Meta.Harness == nil {
		return TurnInfo{}, errors.New("this session's agent has no questions to answer")
	}
	ad := adapterFor(sess.Meta.Harness.Name)
	if ad == nil {
		return TurnInfo{}, fmt.Errorf("unknown harness %q", sess.Meta.Harness.Name)
	}
	message, display := ad.QuestionReply(callID, nil, answers)
	if r.hub.Running(req.SessionID) {
		if info, err := r.hub.Steer(req.SessionID, message, display); err == nil {
			return info, nil
		}
		// The turn ended between the check and the send: it starts a turn below.
	}
	req.Message = message
	info, _, err := r.SubmitTurn(req, SubmitOpts{Display: display})
	return info, err
}

// annotateHarness settles, for a harness session, what its stored cards could not
// know when they were written (see harness.Adapter.Annotate).
func annotateHarness(meta transcript.Meta, msgs []transcript.Message) []transcript.Message {
	if meta.Harness == nil {
		return msgs
	}
	if ad := adapterFor(meta.Harness.Name); ad != nil {
		return ad.Annotate(msgs)
	}
	return msgs
}

// checkCodexRepair looks over a Codex session the first time this process shows
// it, in the background, for commands whose outcome its transcript never got.
func (r *AgentRuntime) checkCodexRepair(sess transcript.Session) {
	h := sess.Meta.Harness
	if h == nil || h.Name != HarnessCodex || h.ThreadID == "" {
		return
	}
	if _, seen := r.codexRepaired.LoadOrStore(sess.Meta.SessionID, true); seen {
		return
	}
	go r.repairCodexSession(sess.Meta.Cwd, sess.Meta.SessionID, h.ThreadID)
}

// repairCodexSession settles shell cards that never learned how their command ended:
// ones left running when their turn ended, and ones an older Paladin closed as unknown
// or interrupted. Codex keeps the real record, so the card gets the real outcome.
// A running command that Codex cannot vouch for, and that no live thread follows
// any more, reads unknown: Paladin lost track of it, and "running" would be a claim.
func (r *AgentRuntime) repairCodexSession(cwd, sessionID, threadID string) {
	sess, err := r.transcripts.Load(cwd, sessionID)
	if err != nil {
		return
	}
	msgs, err := sess.History()
	if err != nil {
		return
	}
	candidates := map[string]string{} // call id -> stored result
	for _, m := range msgs {
		if m.Role == transcript.RoleTool && m.Name != nil && *m.Name == "shell" && m.ToolCallID != nil && codexadapter.Unresolved(m.Content) {
			candidates[*m.ToolCallID] = m.Content
		}
	}
	r.codexMu.Lock()
	live := r.codexThreads[sessionID]
	r.codexMu.Unlock()
	if live != nil {
		live.bg.mu.Lock()
		for id := range live.bg.cmds {
			delete(candidates, id) // it will settle itself
		}
		live.bg.mu.Unlock()
	}
	if len(candidates) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), codexStartTimeout)
	defer cancel()
	client, err := r.codex.Client(ctx)
	if err != nil {
		return
	}
	turns, err := client.ThreadTurns(ctx, threadID)
	if err != nil {
		return
	}
	items := map[string]codex.Raw{}
	for _, turn := range turns {
		for _, it := range turn.List("items") {
			if it.Str("type") == "commandExecution" {
				items[it.Str("id")] = it
			}
		}
	}
	changed := 0
	for id, stored := range candidates {
		var result string
		var failed bool
		if it, ok := items[id]; ok && codexadapter.Settled(it) {
			result, failed = codexadapter.Outcome(it, "")
		} else if strings.Contains(stored, `"running"`) {
			result = codexadapter.UnknownResult(stored)
		} else {
			continue
		}
		if n, err := r.patchToolRow(sessionID, cwd, id, result, failed); err == nil {
			changed += n
		}
	}
	if changed > 0 {
		r.hub.PublishSession(sessionID, "session/refresh")
	}
}
