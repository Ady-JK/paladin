// Session lifecycle: open, list, history, todos, fork, rename, delete.

package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Ady-JK/paladin/core/internal/session"
	"github.com/Ady-JK/paladin/core/internal/syserr"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

type Session struct {
	Cwd       string `json:"cwd"`
	Model     string `json:"model"`
	Provider  string `json:"provider"`
	SessionID string `json:"sessionId"`
	// Revert is the session's pending undo, if any.
	Revert *session.RevertInfo `json:"revert,omitempty"`
	// Worktree is set for sessions working in a managed git worktree.
	Worktree *session.WorktreeInfo `json:"worktree,omitempty"`
	// Subagent is set when the session is a subagent's child session.
	Subagent *session.SubagentInfo `json:"subagent,omitempty"`
}

// runtimeSessions adapts the transcript store to agent.SessionSource.
type runtimeSessions struct{ r *AgentRuntime }

func (s runtimeSessions) LoadSession(cwd, sessionID string) (transcript.Session, error) {
	return s.r.transcripts.Load(cwd, sessionID)
}

func (r *AgentRuntime) OpenSession(sessionID, cwd string) (Session, error) {
	if strings.TrimSpace(sessionID) != "" {
		sess, err := r.transcripts.Load(cwd, sessionID)
		if err != nil {
			return Session{}, err
		}
		provider := ""
		if sess.Meta.Provider != nil {
			provider = *sess.Meta.Provider
		}
		model := ""
		if sess.Meta.Model != nil {
			model = *sess.Meta.Model
		}
		opened := Session{SessionID: sess.Meta.SessionID, Cwd: sess.Meta.Cwd, Provider: provider, Model: model,
			Revert: session.RevertView(sess.Meta.Revert), Worktree: session.WorktreeView(sess.Meta.Worktree),
			Subagent: session.SubagentView(sess.Meta.Subagent)}
		if opened.Subagent != nil {
			opened.Subagent.Status = r.subagents.status(opened.SessionID, opened.Subagent.Status)
		}
		return opened, nil
	}
	if strings.TrimSpace(cwd) == "" {
		return Session{}, fmt.Errorf("cwd is required to create a session")
	}
	entry, provider, err := r.resolveProvider("", "")
	if err != nil {
		return Session{}, err
	}
	sess, err := r.transcripts.Create(cwd, entry.Name, provider.Model())
	if err != nil {
		return Session{}, err
	}
	return Session{SessionID: sess.Meta.SessionID, Cwd: sess.Meta.Cwd, Provider: entry.Name, Model: provider.Model()}, nil
}

func (r *AgentRuntime) ListSessions(cwd string, limit int) ([]session.SessionInfo, error) {
	if limit < 0 {
		limit = 0
	}
	infos, err := r.transcripts.List(cwd, limit)
	if err != nil {
		return nil, err
	}
	out := make([]session.SessionInfo, 0, len(infos))
	for _, info := range infos {
		view := session.InfoFrom(info.Meta, info.MessageCount, info.Title)
		view.Damaged = info.Damaged
		if view.Subagent != nil {
			view.Subagent.Status = r.subagents.status(view.SessionID, view.Subagent.Status)
		}
		out = append(out, view)
	}
	return out, nil
}

func (r *AgentRuntime) SessionHistory(sessionID, cwd string) ([]session.HistoryMessage, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, fmt.Errorf("session id is required")
	}
	sess, err := r.transcripts.Load(cwd, sessionID)
	if err != nil {
		return nil, err
	}
	msgs, err := sess.History()
	if err != nil {
		return nil, err
	}
	return session.HistoryView(visibleMessages(sess.Meta, msgs)), nil
}

// SessionTodos returns the session's activity list for the UI's todo
// hat above the composer: empty when the agent has not planned yet,
// never an error for a missing file.
func (r *AgentRuntime) SessionTodos(sessionID, cwd string) ([]session.TodoEntry, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, fmt.Errorf("session id is required")
	}
	sess, err := r.transcripts.Load(cwd, sessionID)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(sess.Dir, session.TodoFileName))
	if err != nil {
		if os.IsNotExist(err) {
			return []session.TodoEntry{}, nil
		}
		return nil, fmt.Errorf("io: session todos: %s", syserr.Str(err))
	}
	return session.TodosView(data), nil
}

// ClearSessionTodos drops the session's activity list. A missing file
// is a no-op so clearing before the first plan never errors.
func (r *AgentRuntime) ClearSessionTodos(sessionID, cwd string) error {
	if strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("session id is required")
	}
	sess, err := r.transcripts.Load(cwd, sessionID)
	if err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(sess.Dir, session.TodoFileName)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("io: session todos: %s", syserr.Str(err))
	}
	return nil
}

// dropFinishedTodos removes a fully-landed plan at the start of a new
// turn: when the user's next message arrives and every step of the
// stored list is finished or cancelled, the list is dropped so the
// panel goes empty until the agent plans the new work. Resume turns
// never drop — they continue the same work — and anything unfinished
// or unparsable stays put.
func (r *AgentRuntime) dropFinishedTodos(sess transcript.Session, resume bool) {
	if resume {
		return
	}
	data, err := os.ReadFile(filepath.Join(sess.Dir, session.TodoFileName))
	if err != nil {
		return
	}
	if !session.TodosFinished(data) {
		return
	}
	_ = os.Remove(filepath.Join(sess.Dir, session.TodoFileName))
}

func (r *AgentRuntime) DeleteSession(sessionID, cwd string) (bool, error) {
	if strings.TrimSpace(sessionID) == "" {
		return false, fmt.Errorf("session id is required")
	}
	// Forget it first so a finishing child cannot wake it again, then
	// delete its children before stopping it.
	r.subagents.forget(sessionID)
	sess, loadErr := r.transcripts.Load(cwd, sessionID)
	if loadErr == nil {
		if err := r.subagents.deleteChildren(sess); err != nil {
			return false, err
		}
	}
	// A running turn would re-create the transcript when it persists:
	// stop it (and its queue) and let it land before deleting.
	if r.hub.Busy(sessionID) {
		r.hub.StopSession(sessionID, "")
		if !r.hub.WaitIdle(sessionID, 15*time.Second) {
			return false, fmt.Errorf("session %q is still stopping; try again", sessionID)
		}
	}
	// A finished turn's done callback may still be recording its end.
	r.hub.SettleDone(5 * time.Second)
	if loadErr == nil {
		if sa := sess.Meta.Subagent; sa != nil {
			_ = r.transcripts.UpdateMeta(sess.Meta.Cwd, sa.ParentID, func(m *transcript.Meta) error {
				m.Children = slices.DeleteFunc(m.Children, func(id string) bool { return id == sessionID })
				return nil
			})
		}
	}
	if err := r.transcripts.Delete(cwd, sessionID); err != nil {
		return false, err
	}
	return true, nil
}

// ForkSession copies a session into a new one and returns its info.
func (r *AgentRuntime) ForkSession(sessionID, cwd string) (session.SessionInfo, error) {
	// A fork mid-turn copies what is on disk: every finished turn.
	fork, err := r.transcripts.Fork(cwd, sessionID)
	if err != nil {
		return session.SessionInfo{}, err
	}
	msgs, _ := fork.History()
	count := 0
	for _, m := range msgs {
		if m.Visible() {
			count++
		}
	}
	return session.InfoFrom(fork.Meta, count, fork.Meta.Title), nil
}

// RenameSession sets (or clears) a session's title.
func (r *AgentRuntime) RenameSession(sessionID, cwd, title string) error {
	return r.transcripts.Rename(cwd, sessionID, title)
}
