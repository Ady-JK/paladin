// review.go is the memory review loop: turn-count staleness and the
// background review fork.
//
// Learning whether a turn produced a durable fact is a judgement the model
// makes best with the conversation in front of it — but it is also a
// second request per turn, so it cannot run unconditionally. The loop is
// therefore split in two cheap halves:
//
//  1. Staleness is counted in TURNS, not wall-clock time: a session idle
//     overnight has learned nothing, while one long productive turn may
//     have learned a lot. After reviewTurns consecutive turns that never
//     wrote memory, one short nudge paragraph is appended to the prompt
//     telling the agent to save anything durable it noticed. That costs
//     nothing extra and keeps a stale session honest.
//
//  2. The background review fork is a subagent that replays the finished
//     conversation and reports what, if anything, is worth remembering.
//     It is a fork (a child session under the same runtime), not an
//     in-process side channel: it inherits the parent's provider, model
//     and prompt cache, and its tool set is narrowed to memory only, so
//     it can never touch the workspace. Auto-forking costs a second
//     request per review, so it is opt-in via memory.json.
//
// The counter lives beside the session transcript so it survives a
// restart; it is reset by any memory write from the session.

package memory

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Ady-JK/paladin/core/internal/atomicfile"
)

// ReviewFile tracks a session's progress towards a review, beside its
// frozen snapshot.
const ReviewFile = "memory_review.json"

// ReviewState is one session's review bookkeeping.
type ReviewState struct {
	// Turn counts finished turns for this session, so every other number
	// here is a turn index rather than a wall-clock time.
	Turn int `json:"turn"`
	// TurnsSinceWrite counts finished turns since the session last wrote
	// memory. It drives the nudge.
	TurnsSinceWrite int `json:"turnsSinceWrite"`
	// PendingWrite is set by the memory tool during a turn and consumed
	// when that turn ends, so a write anywhere in a turn resets the
	// counter exactly once.
	PendingWrite bool `json:"pendingWrite,omitempty"`
	// ReviewedTurn is the turn the last background review covered.
	ReviewedTurn int `json:"reviewedTurn,omitempty"`
}

// procReview serializes read-modify-write per session file.
var procReview sync.Map

func reviewLock(path string) func() {
	v, _ := procReview.LoadOrStore(path, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// LoadReviewState reads a session's counter; a missing or unreadable file
// is zero, so a corrupt counter can never wedge the loop.
func LoadReviewState(sessionDir string) ReviewState {
	var st ReviewState
	if strings.TrimSpace(sessionDir) == "" {
		return st
	}
	data, err := os.ReadFile(filepath.Join(sessionDir, ReviewFile))
	if err != nil {
		return st
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return ReviewState{}
	}
	return st
}

func saveReviewState(sessionDir string, st ReviewState) error {
	if strings.TrimSpace(sessionDir) == "" {
		return nil
	}
	// A session deleted while its turn was finishing has nowhere to record
	// this: writing would recreate the folder the delete just removed (and
	// race the delete). The counter is recomputed from the next turn.
	if !dirExists(sessionDir) {
		return nil
	}
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return atomicfile.Write(filepath.Join(sessionDir, ReviewFile), append(data, '\n'))
}

// NoteWrite records that the memory tool changed a store during the
// running turn. The counter itself is settled by EndTurn.
func NoteWrite(sessionDir string) error {
	if strings.TrimSpace(sessionDir) == "" {
		return nil
	}
	unlock := reviewLock(sessionDir)
	defer unlock()
	st := LoadReviewState(sessionDir)
	st.PendingWrite = true
	return saveReviewState(sessionDir, st)
}

// EndTurn settles one finished turn: a turn that wrote memory resets the
// counter, any other turn advances it. That is what makes staleness a
// count of turns rather than a clock.
func EndTurn(sessionDir string) (ReviewState, error) {
	if strings.TrimSpace(sessionDir) == "" {
		return ReviewState{}, nil
	}
	unlock := reviewLock(sessionDir)
	defer unlock()
	st := LoadReviewState(sessionDir)
	st.Turn++
	if st.PendingWrite {
		st.PendingWrite = false
		st.TurnsSinceWrite = 0
	} else {
		st.TurnsSinceWrite++
	}
	if err := saveReviewState(sessionDir, st); err != nil {
		return st, fmt.Errorf("io: write memory review state: %w", err)
	}
	return st, nil
}

// MarkReviewed records that a background review covered turn.
func MarkReviewed(sessionDir string, turn int) error {
	if strings.TrimSpace(sessionDir) == "" {
		return nil
	}
	unlock := reviewLock(sessionDir)
	defer unlock()
	st := LoadReviewState(sessionDir)
	if turn > st.ReviewedTurn {
		st.ReviewedTurn = turn
	}
	return saveReviewState(sessionDir, st)
}

// DueForNudge reports whether a store has gone reviewTurns turns without
// a write, and returns the paragraph to append to the prompt when so.
// An empty string means "nothing to say", which keeps the prompt
// byte-stable and cacheable across turns that do not need the reminder.
func (b *Bank) DueForNudge(sessionDir string) string {
	threshold := b.cfg.ReviewTurns
	if threshold <= 0 {
		return ""
	}
	st := LoadReviewState(sessionDir)
	if st.TurnsSinceWrite < threshold {
		return ""
	}
	return fmt.Sprintf("%d turns have passed since anything was saved to memory.\n%s",
		st.TurnsSinceWrite, reviewNudge)
}

// reviewNudge is the reminder text. It names the threshold so the model
// can weigh it, and the three targets so no wrapper text is needed.
const reviewNudge = `MEMORY REVIEW: %d turns have passed since anything was saved to memory.
Before you finish, consider whether this conversation produced something durable:
a preference, a convention, a build/test command, an architecture fact, a gotcha
the next session would otherwise rediscover. If it did, call the memory tool and
save it — ` + "`user`" + ` for who the user is, ` + "`global`" + ` for facts that hold everywhere,
` + "`project`" + ` for this folder. If nothing durable came up, say nothing and carry on.
Do not save task progress or anything already stored.`

// ReviewTask is the assignment handed to a review fork. It names the
// three targets, forbids workspace changes, and asks for silence when
// there is nothing to save — a review that finds nothing must not spam
// the parent with a summary.
const ReviewTask = `Review the conversation above and decide what is worth remembering.

Save, with the memory tool, only durable facts that will still matter in a future session:
- ` + "`user`" + ` — who the user is: role, preferences, communication style, constraints on how you work.
- ` + "`global`" + ` — notes that hold everywhere: machine setup, tools, versions, lessons learned.
- ` + "`project`" + ` — facts about this workspace folder: build/test/lint commands, conventions, architecture, gotchas.

Rules:
- Prefer patching an existing entry over adding a near-duplicate; remove entries that are now wrong.
- Never save task progress, a todo state, temporary debugging findings, secrets, or raw log output.
- Entries must be one short sentence or two, self-contained, and understandable without this conversation.
- You cannot change the workspace and must not try; the memory tool is your only write.
- If nothing passes the bar, make no calls and reply with exactly: NOTHING

Otherwise reply with one line naming what you saved, for the parent's log.`

// ShouldForkReview reports whether a background review is due for this
// session: auto-forking is on in memory.json, and there are new turns
// since the last review.
func (b *Bank) ShouldForkReview(sessionDir string) bool {
	if !b.cfg.AutoReview {
		return false
	}
	if strings.TrimSpace(sessionDir) == "" {
		return false
	}
	st := LoadReviewState(sessionDir)
	return st.Turn > st.ReviewedTurn
}

// ErrDisabled reports a store switched off in memory.json, so callers can
// tell "you may not" from "that failed".
var ErrDisabled = errors.New("memory target is switched off")

// recallMinWords is how much overlap a message needs before an entry is
// recalled. Below this the match is noise (one shared word), and a recall
// that fires on everything is worse than none: it trains the model to
// ignore the reminder.
const recallMinWords = 2

// Match finds the entry a user message is most clearly about, across the
// enabled scopes, for recall injection into the prompt.
//
// The score is how many distinct words the message shares with an entry,
// on the same folded form the tool uses for old_text, so "the build's
// cache" matches an entry saying "build cache". An entry that shares
// fewer than recallMinWords distinct words is not a match; ties go to the
// project scope (the most specific), then to the longer overlap.
func (b *Bank) Match(cwd, message string) (string, Target, bool) {
	words := recallWords(message)
	if len(words) < recallMinWords {
		return "", "", false
	}
	type scored struct {
		entry  string
		target Target
		score  int
		order  int
	}
	var best scored
	found := false
	// Project first so it wins a tie against the broader scopes.
	for order, t := range []Target{TargetProject, TargetGlobal, TargetUser} {
		if t == TargetProject && strings.TrimSpace(cwd) == "" {
			continue
		}
		if !b.cfg.TargetEnabled(t) {
			continue
		}
		res, err := b.Read(t, cwd)
		if err != nil {
			continue
		}
		for _, entry := range res.Entries {
			if Scan(entry) != "" {
				continue
			}
			score := overlap(words, recallWords(entry))
			if score < recallMinWords {
				continue
			}
			// A longer shared overlap wins; on equal overlap the earlier
			// scope (project) wins, then the earlier entry.
			if found && (score < best.score || (score == best.score && order >= best.order)) {
				continue
			}
			best, found = scored{entry: entry, target: t, score: score, order: order}, true
		}
	}
	if !found {
		return "", "", false
	}
	return best.entry, best.target, true
}

// recallWords is the folded word set of a text, lowercased, dropping the
// words too common to mean anything on their own.
func recallWords(text string) map[string]bool {
	out := map[string]bool{}
	for _, w := range wordRE.FindAllString(normalizeForMatch(strings.ToLower(text)), -1) {
		if len(w) < 3 || recallStopWords[w] {
			continue
		}
		out[w] = true
	}
	return out
}

// recallStopWords are words that appear in nearly every entry or request,
// so matching on them says nothing about relevance.
var recallStopWords = map[string]bool{
	"the": true, "and": true, "that": true, "this": true, "with": true, "for": true,
	"you": true, "your": true, "are": true, "was": true, "have": true, "has": true,
	"can": true, "will": true, "would": true, "should": true, "does": true, "did": true,
	"not": true, "but": true, "all": true, "any": true, "how": true, "what": true,
	"when": true, "where": true, "which": true, "there": true, "their": true, "them": true,
	"from": true, "into": true, "than": true, "then": true, "also": true, "use": true,
	"used": true, "using": true, "make": true, "made": true, "run": true, "runs": true,
	"please": true, "help": true, "need": true, "want": true, "like": true, "just": true,
}

// overlap counts the distinct shared words.
func overlap(a, b map[string]bool) int {
	n := 0
	for w := range a {
		if b[w] {
			n++
		}
	}
	return n
}

// dirExists reports whether path is still a directory. Used before writing
// per-session state, so a deleted session is never recreated by a turn
// that was finishing when the delete happened.
func dirExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}
