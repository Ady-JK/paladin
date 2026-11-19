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
	if sess.Meta.Subagent != nil || sess.Meta.Harness != nil {
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
	kind, ok := r.findConfiguredSubagent("memory-review")
	if !ok {
		kind = reviewKind()
	}
	if _, err := r.subagents.spawn(sess.Meta.SessionID, spawnArgs{
		Kind:      kind,
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
//
// An entry joins a conversation once. Its hash is kept beside the
// transcript (memory.RecalledFile) when the reminder is appended, and a
// later message matching the same entry only tells the user it was
// skipped; compaction and undo clear the hashes along with the messages.
func (r *AgentRuntime) memoryRecall(req agent.Request, cwd, sessionDir string) []agent.Notice {
	if r.memory == nil || req.Resume || req.Compact || strings.TrimSpace(req.Message) == "" {
		return nil
	}
	entry, target, ok := r.memory.Match(cwd, req.Message)
	if !ok {
		return nil
	}
	hash := memory.RecallHash(target, entry)
	if memory.IsRecalled(sessionDir, hash) {
		return []agent.Notice{{
			CallID:    "memory-recall-skipped-" + hash,
			Name:      "memory",
			Transient: true,
			Content:   "Skipped recalling " + string(target) + " memory: this entry is already in the conversation.",
		}}
	}
	args, err := jsonx.Marshal(map[string]string{"action": "recall", "target": string(target)})
	if err != nil {
		return nil
	}
	return []agent.Notice{{
		CallID:    "memory-recall-" + hash,
		Name:      "memory",
		Arguments: string(args),
		Content:   memory.RecallNote(target, entry),
		Appended: func() {
			if err := memory.MarkRecalled(sessionDir, hash); err != nil {
				log.Printf("memory: record recalled entry: %v", err)
			}
		},
	}}
}

// syncRecalled makes the recalled-entry record match a transcript that was
// truncated or restored: an entry stays recalled only if its reminder is
// still in the part of the conversation the model sees (after the last
// compaction). Anything unreadable errs towards recalling again.
func syncRecalled(sess transcript.Session) {
	var kept []string
	if msgs, err := sess.History(); err == nil {
		start := 0
		for i, m := range msgs {
			if m.Compaction != nil {
				start = i
			}
		}
		for _, m := range msgs[start:] {
			if m.Role == transcript.RoleUser || (m.Role == transcript.RoleTool && m.IsNotify) {
				kept = append(kept, m.Content)
			}
		}
	} else {
		log.Printf("memory: read %s to rebuild its recalled entries: %v", sess.Meta.SessionID, err)
	}
	if err := memory.ResetRecalled(sess.Dir, memory.RecalledIn(kept)); err != nil {
		log.Printf("memory: reset recalled entries for %s: %v", sess.Meta.SessionID, err)
	}
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
