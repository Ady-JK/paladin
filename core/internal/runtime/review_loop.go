// review_loop.go is the runtime half of the memory review loop: it
// advances the turn counter when a turn finishes, and — only when
// memory.json turns it on — forks a review subagent that replays the
// conversation and may write memory and nothing else.
//
// The fork is a real child session (see subagent_lifecycle.go), so it
// inherits the parent's provider, model and cached prompt, appears in the
// agent list like any other child, and is bounded by the same per-parent
// child limit. Its tool set is the review kind's allowlist, narrowed by
// the parent's switches: `memory` and the read-only inspection tools it
// needs to check a fact, never write/edit or a mutating shell.

package runtime

import (
	"log"
	"strings"

	"github.com/Ady-JK/paladin/core/internal/memory"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

// afterTurn runs for every finished or dropped turn.
func (r *AgentRuntime) afterTurn(info TurnInfo) {
	if r.memory == nil || info.SessionID == "" {
		return
	}
	sess, err := r.transcripts.Load("", info.SessionID)
	if err != nil && strings.TrimSpace(info.Cwd) != "" {
		sess, err = r.transcripts.Load(info.Cwd, info.SessionID)
	}
	if err != nil {
		return
	}
	// A child session's turns are not the user's work; only top-level
	// sessions carry the review counter.
	if sess.Meta.Subagent != nil {
		return
	}
	if _, err := memory.EndTurn(sess.Dir); err != nil {
		log.Printf("memory: review state for %s: %v", sess.Meta.SessionID, err)
	}
	r.maybeForkReview(sess, info)
}

// maybeForkReview spawns the review child when autoReview is on and this
// session has unreviewed turns. Failures are logged, never surfaced: a
// review is an optimization, and the turn it follows already succeeded.
func (r *AgentRuntime) maybeForkReview(sess transcript.Session, info TurnInfo) {
	if info.State != TurnDone || strings.TrimSpace(info.Error) != "" {
		return
	}
	if !r.memory.ShouldForkReview(sess.Dir) {
		return
	}
	turn := memory.LoadReviewState(sess.Dir).Turn
	if _, err := r.subagents.spawn(sess.Meta.SessionID, spawnArgs{
		Kind:      reviewKind(),
		Task:      memory.ReviewTask,
		Name:      "memory-review",
		ForkTurns: ForkAll,
	}); err != nil {
		log.Printf("memory: review fork for %s: %v", sess.Meta.SessionID, err)
		return
	}
	if err := memory.MarkReviewed(sess.Dir, turn); err != nil {
		log.Printf("memory: mark reviewed for %s: %v", sess.Meta.SessionID, err)
	}
}
