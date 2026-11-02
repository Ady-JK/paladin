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

	"github.com/Ady-JK/paladin/core/internal/agent"
	"github.com/Ady-JK/paladin/core/internal/jsonx"
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

// memoryRecall is the point where a relevant stored entry surfaces in a
// turn without the agent having to call the memory tool.
//
// The frozen snapshot already carries every entry, so recall is not about
// adding text that is missing — it is about attention. Once a store grows
// to dozens of entries the relevant line is easy to miss, so when the
// incoming message clearly matches one entry, that entry is repeated as
// one short reminder naming the store it came from.
//
// It is deliberately quiet: at most one entry per turn, only when the
// message shares at least two content words with it, and only for a
// message the user actually typed (a resume, a compaction, or a tool-only
// continuation recalls nothing). That keeps the prompt byte-stable for the
// common case and keeps the reminder meaningful when it does appear.
func (r *AgentRuntime) memoryRecall(req agent.Request, cwd string) []agent.Notice {
	if r.memory == nil || req.Resume || req.Compact || strings.TrimSpace(req.Message) == "" {
		return nil
	}
	entry, target, ok := r.memory.Match(cwd, req.Message)
	if !ok {
		return nil
	}
	args, err := jsonx.Marshal(map[string]string{"action": "recall", "target": string(target)})
	if err != nil {
		return nil
	}
	return []agent.Notice{{
		CallID:    "memory-recall-" + string(target),
		Name:      "memory",
		Arguments: string(args),
		Content:   "Recalled from " + string(target) + " memory — this request looks related:\n" + entry,
	}}
}

// withRecall chains the recall reminder in front of the subagent mailbox,
// so one callable answers the agent's Notices hook. Recall fires exactly
// once: the agent drains notices at the top of a round and again when a
// text round finishes, and a reminder that came back both times would
// keep the turn alive forever.
func withRecall(recall []agent.Notice, next func() []agent.Notice) func() []agent.Notice {
	return func() []agent.Notice {
		out := recall
		recall = nil
		if next != nil {
			out = append(out, next()...)
		}
		return out
	}
}
