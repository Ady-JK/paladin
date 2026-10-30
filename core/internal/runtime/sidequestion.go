package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Ady-JK/paladin/core/internal/agent"
	"github.com/Ady-JK/paladin/core/internal/inference"
	"github.com/Ady-JK/paladin/core/internal/session"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

// sideQuestionTimeout caps one /btw answer.
const sideQuestionTimeout = 3 * time.Minute

type SideQuestionRequest struct {
	SessionID string `json:"sessionId"`
	Cwd       string `json:"cwd"`
	Question  string `json:"question"`
	// ChatID continues a saved side chat; empty starts a new one.
	ChatID   string `json:"chatId,omitempty"`
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
}

type SideAnswer struct {
	ChatID string `json:"chatId"`
	Answer string `json:"answer"`
	Model  string `json:"model"`
}

// sideQuestionPrompt wraps a /btw question so the model answers it
// from what it already knows instead of resuming the main task.
func sideQuestionPrompt(question string) string {
	return "<side_question>\nThe user is asking a quick side question about this conversation " +
		"while the main task carries on separately. Answer it briefly and directly from what " +
		"you already know. You have no tools here, and this exchange is not added to the main " +
		"conversation, so do not continue the task or promise to do anything.\n\n" +
		"Question: " + question + "\n</side_question>"
}

// SideQuestion answers a /btw question about a session without adding
// it to the transcript. It sees the running turn's conversation when
// one is live, else the saved history, plus the side chat's earlier
// exchanges; it gets no tools and is billed to the session like any
// other call. The exchange is saved to the side chat (a new one when
// req.ChatID is empty) only once the answer arrives, so a failed first
// question leaves nothing behind. One question per chat runs at a time.
func (r *AgentRuntime) SideQuestion(ctx context.Context, req SideQuestionRequest) (SideAnswer, error) {
	question := strings.TrimSpace(req.Question)
	if question == "" {
		return SideAnswer{}, errors.New("question is required")
	}
	sess, err := r.transcripts.Load(req.Cwd, req.SessionID)
	if err != nil {
		return SideAnswer{}, err
	}
	chat := transcript.NewSideChat(question)
	if req.ChatID != "" {
		if chat, err = transcript.LoadSideChat(sess.Dir, req.ChatID); err != nil {
			return SideAnswer{}, err
		}
	}
	if !r.claimSideChat(chat.ID) {
		return SideAnswer{}, errors.New("side chat is busy: wait for its answer first")
	}
	defer r.releaseSideChat(chat.ID)
	provider, err := runtimeProviders{r}.ResolveProvider(req.Provider, req.Model)
	if err != nil {
		return SideAnswer{}, err
	}
	messages := closeRunningCalls(r.liveConversation(sess.Meta.SessionID))
	if messages == nil {
		history, err := sess.History()
		if err != nil {
			return SideAnswer{}, err
		}
		messages = agent.LlmMessages(history, true)
	}
	for _, m := range chat.Messages {
		content := m.Content
		if m.Role == transcript.RoleUser {
			content = sideQuestionPrompt(content)
		}
		messages = append(messages, inference.LlmMessage{Role: string(m.Role), Content: content})
	}
	messages = append(messages, inference.LlmMessage{Role: "user", Content: sideQuestionPrompt(question)})
	system := agent.ComposeSystem(r.systemPrompt(""), sess, runtimeSections{r})

	ctx, cancel := context.WithTimeout(ctx, sideQuestionTimeout)
	defer cancel()
	defer context.AfterFunc(r.sideCtx, cancel)()
	resp, err := provider.CompleteTools(ctx, system, messages, nil, sess.Meta.SessionID)
	if resp.Usage != nil {
		session.RecordCall(sess.UsagePath(), sess.Meta.SessionID, provider.Model(), agent.UsageCounters(*resp.Usage))
	}
	if err != nil {
		return SideAnswer{}, fmt.Errorf("side question: %w", err)
	}
	answer := strings.TrimSpace(resp.Content)
	if answer == "" {
		return SideAnswer{}, errors.New("the model returned no answer")
	}
	// Reload so a delete during the call is not undone by this save.
	if req.ChatID != "" {
		if chat, err = transcript.LoadSideChat(sess.Dir, req.ChatID); err != nil {
			return SideAnswer{}, err
		}
	}
	chat.Add(transcript.RoleUser, question, "")
	chat.Add(transcript.RoleAssistant, answer, provider.Model())
	if err := transcript.SaveSideChat(sess.Dir, chat); err != nil {
		return SideAnswer{}, err
	}
	return SideAnswer{ChatID: chat.ID, Answer: answer, Model: provider.Model()}, nil
}

func (r *AgentRuntime) claimSideChat(id string) bool {
	r.liveMu.Lock()
	defer r.liveMu.Unlock()
	if r.sideBusy[id] {
		return false
	}
	r.sideBusy[id] = true
	return true
}

func (r *AgentRuntime) releaseSideChat(id string) {
	r.liveMu.Lock()
	defer r.liveMu.Unlock()
	delete(r.sideBusy, id)
}

// ListSideChats lists a session's saved side chats, newest first.
func (r *AgentRuntime) ListSideChats(sessionID, cwd string) ([]transcript.SideChatInfo, error) {
	sess, err := r.transcripts.Load(cwd, sessionID)
	if err != nil {
		return nil, err
	}
	return transcript.ListSideChats(sess.Dir)
}

func (r *AgentRuntime) LoadSideChat(sessionID, cwd, chatID string) (transcript.SideChat, error) {
	sess, err := r.transcripts.Load(cwd, sessionID)
	if err != nil {
		return transcript.SideChat{}, err
	}
	return transcript.LoadSideChat(sess.Dir, chatID)
}

func (r *AgentRuntime) DeleteSideChat(sessionID, cwd, chatID string) error {
	sess, err := r.transcripts.Load(cwd, sessionID)
	if err != nil {
		return err
	}
	return transcript.DeleteSideChat(sess.Dir, chatID)
}

func (r *AgentRuntime) setLiveConversation(sessionID string, messages []inference.LlmMessage) {
	r.liveMu.Lock()
	defer r.liveMu.Unlock()
	if messages == nil {
		delete(r.live, sessionID)
		return
	}
	r.live[sessionID] = messages
}

// liveConversation is a copy of the running turn's latest model input,
// nil when the session has no turn running.
func (r *AgentRuntime) liveConversation(sessionID string) []inference.LlmMessage {
	r.liveMu.Lock()
	defer r.liveMu.Unlock()
	if m, ok := r.live[sessionID]; ok {
		return append([]inference.LlmMessage(nil), m...)
	}
	return nil
}

// runningToolContent stands in for the result of a call still running
// when /btw is asked; providers reject a tool call with no result.
const runningToolContent = "(still running — no result yet)"

// closeRunningCalls gives every tool call in the last assistant message
// that has no result yet a placeholder one.
func closeRunningCalls(messages []inference.LlmMessage) []inference.LlmMessage {
	last := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "assistant" {
			last = i
			break
		}
	}
	if last < 0 || len(messages[last].ToolCalls) == 0 {
		return messages
	}
	answered := map[string]bool{}
	for _, m := range messages[last+1:] {
		if m.Role == "tool" && m.ToolCallID != nil {
			answered[*m.ToolCallID] = true
		}
	}
	for _, c := range messages[last].ToolCalls {
		if answered[c.ID] {
			continue
		}
		id, name := c.ID, c.Name
		messages = append(messages, inference.LlmMessage{Role: "tool", Content: runningToolContent, ToolCallID: &id, Name: &name})
	}
	return messages
}
