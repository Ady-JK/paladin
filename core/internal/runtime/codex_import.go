package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Ady-JK/paladin/core/internal/agent"
	"github.com/Ady-JK/paladin/core/internal/codex"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

const codexImportTimeout = 60 * time.Second

// DeleteCodexThread removes a saved Codex thread unless a Paladin session owns it.
func (r *AgentRuntime) DeleteCodexThread(threadID string) error {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return errors.New("a Codex thread id is required")
	}
	metas, err := r.transcripts.ListMeta("")
	if err != nil {
		return err
	}
	for _, meta := range metas {
		if meta.Harness != nil && meta.Harness.Name == HarnessCodex && meta.Harness.ThreadID == threadID {
			return errors.New("delete the linked Paladin session first")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), codexImportTimeout)
	defer cancel()
	client, err := r.codex.Client(ctx)
	if err != nil {
		return err
	}
	return client.Call(ctx, "thread/delete", map[string]any{"threadId": threadID}, nil)
}

// CodexThreadQuery narrows the list of Codex's saved threads.
type CodexThreadQuery struct {
	Cwd    string
	Search string
	Cursor string
}

// CodexThreadView is one thread Codex has saved, from any client (the Codex
// CLI, its editor extension, another app).
type CodexThreadView struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Preview   string `json:"preview"`
	Cwd       string `json:"cwd"`
	Model     string `json:"model,omitempty"`
	Source    string `json:"source,omitempty"`
	UpdatedAt int64  `json:"updatedAt"`
	// SessionID names the Paladin session that already holds this thread.
	SessionID string `json:"sessionId,omitempty"`
}

// CodexThreadPage is a page of saved threads.
type CodexThreadPage struct {
	Threads []CodexThreadView `json:"threads"`
	Next    string            `json:"next,omitempty"`
}

// CodexThreads lists the threads Codex has saved, most recent first, marking
// the ones a Paladin session already holds.
func (r *AgentRuntime) CodexThreads(q CodexThreadQuery) (CodexThreadPage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), codexImportTimeout)
	defer cancel()
	client, err := r.codex.Client(ctx)
	if err != nil {
		return CodexThreadPage{}, err
	}
	threads, next, err := client.ListThreads(ctx, codex.ThreadQuery{Cwd: q.Cwd, Search: q.Search, Cursor: q.Cursor})
	if err != nil {
		return CodexThreadPage{}, err
	}
	held := r.codexThreadSessions()
	page := CodexThreadPage{Threads: make([]CodexThreadView, 0, len(threads)), Next: next}
	for _, t := range threads {
		title := strings.TrimSpace(t.Name)
		if title == "" {
			title = strings.TrimSpace(t.Preview)
		}
		page.Threads = append(page.Threads, CodexThreadView{ID: t.ID, Title: title, Preview: t.Preview, Cwd: t.Cwd, Model: t.Model,
			Source: t.Source, UpdatedAt: t.UpdatedAt, SessionID: held[t.ID].SessionID})
	}
	return page, nil
}

// codexThreadSessions maps Codex thread ids to the Paladin sessions holding them.
func (r *AgentRuntime) codexThreadSessions() map[string]transcript.Meta {
	out := map[string]transcript.Meta{}
	metas, err := r.transcripts.ListMeta("")
	if err != nil {
		return out
	}
	for _, m := range metas {
		if m.Harness != nil && m.Harness.Name == HarnessCodex && m.Harness.ThreadID != "" {
			out[m.Harness.ThreadID] = m
		}
	}
	return out
}

// ImportCodexThread opens a Codex thread another client started as a Paladin
// session: its history is brought in and its next message continues the very
// thread. A thread already imported opens the session that holds it. cwd is
// the folder to use when the thread's own no longer exists.
func (r *AgentRuntime) ImportCodexThread(threadID, cwd string) (Session, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return Session{}, errors.New("a Codex thread id is required")
	}
	if held, ok := r.codexThreadSessions()[threadID]; ok {
		return r.OpenSession(held.SessionID, held.Cwd)
	}
	ctx, cancel := context.WithTimeout(context.Background(), codexImportTimeout)
	defer cancel()
	client, err := r.codex.Client(ctx)
	if err != nil {
		return Session{}, err
	}
	thread, err := client.ReadThread(ctx, threadID)
	if err != nil {
		return Session{}, err
	}
	dir := thread.Cwd
	if st, serr := os.Stat(dir); dir == "" || serr != nil || !st.IsDir() {
		dir = strings.TrimSpace(cwd)
	}
	if dir == "" {
		return Session{}, fmt.Errorf("the thread's folder %q is gone; open a folder to import it into", thread.Cwd)
	}
	sess, created, err := r.ensureCodexSession(thread, dir, true)
	if err != nil {
		return Session{}, err
	}
	if !created {
		return r.OpenSession(sess.Meta.SessionID, sess.Meta.Cwd)
	}
	if _, err := r.SyncSession(sess.Meta.SessionID, sess.Meta.Cwd); err != nil {
		return Session{}, err
	}
	return harnessSessionView(sess), nil
}

// shortenTitle keeps a session title to a single short line.
func shortenTitle(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	if r := []rune(s); len(r) > transcript.MaxTitleRunes {
		return string(r[:transcript.MaxTitleRunes-1]) + "…"
	}
	return s
}

// codexTurnMessages turns one stored Codex turn into the transcript rows its
// live run would have written, by replaying its items through the same code.
func codexTurnMessages(turn codex.Raw) []transcript.Message {
	tx := make(chan agent.ChatChunk, 256)
	drained := make(chan struct{})
	go func() {
		for range tx {
		}
		close(drained)
	}()
	run := &codexRun{tx: tx, planRow: -1, status: turn.Str("status"), streamed: map[string]int{}, summary: map[string]int64{},
		tools: map[string]*codexTool{}, approvals: map[string]int{}}
	for _, item := range turn.List("items") {
		switch item.Str("type") {
		case "userMessage":
			run.flushText(nil)
			if text := userText(item); text != "" {
				run.records = append(run.records, transcript.UserMessage(text))
			}
		case "plan":
			run.messageDone(item)
		case "agentMessage":
			// Goes through the live path: a question asked without stopping the work
			// becomes its card, any other message is text.
			run.itemCompleted(item)
		case "reasoning":
			var parts []string
			for _, p := range item.Strings("summary") {
				if p = strings.TrimSpace(p); p != "" {
					parts = append(parts, p)
				}
			}
			if len(parts) > 0 {
				if run.reasoning.Len() > 0 {
					run.reasoning.WriteString("\n\n")
				}
				run.reasoning.WriteString(strings.Join(parts, "\n\n"))
			}
		default:
			run.itemCompleted(item)
		}
	}
	run.closeOpenTools()
	run.flushText(nil)
	close(tx)
	<-drained
	return run.records
}

// userText is the text of a stored user message.
func userText(item codex.Raw) string {
	var parts []string
	for _, c := range item.List("content") {
		if c.Str("type") == "text" && strings.TrimSpace(c.Str("text")) != "" {
			parts = append(parts, c.Str("text"))
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}
