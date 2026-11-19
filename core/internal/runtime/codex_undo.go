package runtime

import (
	"context"
	"log"
	"time"

	"github.com/Ady-JK/paladin/core/internal/codex"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

const codexRewindTimeout = 30 * time.Second

// codexRewindTarget finds the Codex turn an undo to index starts dropping: the
// first turn id recorded on or after that row. tagged is false for a session
// with no turn ids at all (one from before they were recorded), which cannot be
// reverted precisely.
func codexRewindTarget(msgs []transcript.Message, index int) (beforeTurn string, tagged bool) {
	for _, m := range msgs {
		if m.HarnessTurn != "" {
			tagged = true
			break
		}
	}
	for i := max(index, 0); i < len(msgs); i++ {
		if msgs[i].HarnessTurn != "" {
			return msgs[i].HarnessTurn, true
		}
	}
	return "", tagged
}

// codexRewind takes a Codex session's thread back to just before the row at
// index, as an undo does to the transcript. When Codex cannot (a thread with
// legacy history, or a session from before turn ids were recorded) it lets go
// of the thread instead, so the next message starts a new one told what is
// left of the conversation: the undo must not be blocked by it.
func (r *AgentRuntime) codexRewind(sess *transcript.Session, index int) {
	if sess.Meta.Harness == nil || sess.Meta.Harness.Name != HarnessCodex || sess.Meta.Harness.ThreadID == "" {
		return
	}
	msgs, err := sess.History()
	if err != nil || index >= len(msgs) {
		return
	}
	beforeTurn, tagged := codexRewindTarget(msgs, index)
	if tagged && beforeTurn == "" {
		return // nothing Codex knows about comes after the undo point
	}
	if beforeTurn != "" {
		if err := r.revertCodexThread(sess, beforeTurn); err == nil {
			return
		} else {
			log.Printf("codex: revert of %s to before %s: %v; the session moves to a new thread", sess.Meta.SessionID, beforeTurn, err)
		}
	}
	r.dropCodexThread(sess)
}

// revertCodexThread loads the session's thread if this process has not, and
// reverts it.
func (r *AgentRuntime) revertCodexThread(sess *transcript.Session, beforeTurn string) error {
	ctx, cancel := context.WithTimeout(context.Background(), codexRewindTimeout)
	defer cancel()
	client, err := r.codex.Client(ctx)
	if err != nil {
		return err
	}
	choice, _ := client.Requirements().Choose(codex.PresetReadOnly)
	th, _, err := r.codexThreadFor(ctx, sess, sess.Meta.Cwd, codex.ThreadParams{Cwd: sess.Meta.Cwd, ApprovalPolicy: choice.Approval, Sandbox: choice.Sandbox})
	if err != nil {
		return err
	}
	if err := client.RevertThread(ctx, th.threadID, beforeTurn); err != nil {
		return err
	}
	// The thread's running total is about to fall; the next notice has nothing to diff against.
	th.usageBase = nil
	return nil
}

// dropCodexThread detaches the session from its Codex thread.
func (r *AgentRuntime) dropCodexThread(sess *transcript.Session) {
	r.forgetCodexThread(sess.Meta.SessionID)
	_ = r.transcripts.UpdateMeta(sess.Meta.Cwd, sess.Meta.SessionID, func(m *transcript.Meta) error {
		if m.Harness != nil {
			m.Harness.ThreadID = ""
		}
		return nil
	})
	if sess.Meta.Harness != nil {
		sess.Meta.Harness.ThreadID = ""
	}
}
