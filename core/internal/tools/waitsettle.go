// waitsettle.go settles the activity-list steps a wait call covers.
//
// The unfinished-plan guard fires when a turn ends with open steps, because
// that normally means work was abandoned. Waiting is the one honest exception:
// the step is not abandoned, it is blocked on a job or a subagent that the
// wait call is holding the turn for. Rather than teach the guard about jobs,
// the wait tool marks those steps finished — the step's outcome is decided
// by the result the wait returns, and a plan that reads as "waiting" would
// otherwise fail the turn it was trying to pause.

package tools

import (
	"fmt"
	"strings"
	"unicode"
)

// SettleTodos marks the steps the model named in a wait call as finished.
// Each name matches a step by exact content first, then by case-insensitive
// containment, so "the Go test run" settles "Run the Go tests". A name that
// matches nothing is ignored: a stale name must not fail the wait, and the
// plan guard will still nudge for any step genuinely left open.
func SettleTodos(sessionDir string, steps []string) error {
	if strings.TrimSpace(sessionDir) == "" || len(steps) == 0 {
		return nil
	}
	todos, err := loadTodos(sessionDir)
	if err != nil || len(todos) == 0 {
		return err
	}
	changed := false
	for _, want := range steps {
		want = strings.TrimSpace(want)
		if want == "" {
			continue
		}
		if i := matchTodo(todos, want); i >= 0 && todos[i].Status != TodoFinished {
			todos[i].Status = TodoFinished
			changed = true
		}
	}
	if !changed {
		return nil
	}
	if err := saveTodos(sessionDir, todos); err != nil {
		return fmt.Errorf("settle the activity list: %w", err)
	}
	return nil
}

// matchTodo finds the one step a wait call names. Exact text wins, so a short
// name never settles the wrong row. Otherwise the step whose words cover the
// name best is taken — not a substring test, because a model paraphrases the
// step it means ("the Go test run" for "Run the Go tests") and a substring
// match would miss it. Two steps that fit equally well are refused: guessing
// would settle a step the agent did not mean to close.
func matchTodo(todos []TodoItem, want string) int {
	want = strings.TrimSpace(want)
	if want == "" {
		return -1
	}
	for i, todo := range todos {
		if strings.EqualFold(strings.TrimSpace(todo.Content), want) {
			return i
		}
	}
	wantWords := todoWords(want)
	if len(wantWords) == 0 {
		return -1
	}
	best, bestScore := -1, 0
	tie := false
	for i, todo := range todos {
		score := overlap(wantWords, todoWords(todo.Content))
		if score < len(wantWords) {
			continue // the step does not account for every word named
		}
		switch {
		case score > bestScore:
			best, bestScore, tie = i, score, false
		case score == bestScore:
			tie = true
		}
	}
	if tie {
		return -1
	}
	return best
}

// todoWords lower-cases a phrase into comparable stems, dropping the short
// function words ("the", "a", "to") that carry no meaning and would otherwise
// decide a match. A plural is folded to its stem so "tests" and "test" agree:
// a model paraphrasing a step changes its inflection far more often than its
// subject.
func todoWords(s string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if stopWords[w] || (len(w) < 3 && w != "go" && w != "js" && w != "ui") {
			continue
		}
		out = append(out, stem(w))
	}
	return out
}

// stopWords carry no meaning on their own; a match must not rest on them.
var stopWords = map[string]bool{
	"the": true, "a": true, "an": true, "to": true, "of": true, "and": true, "or": true,
	"for": true, "in": true, "on": true, "it": true, "its": true, "this": true, "that": true,
	"with": true, "by": true, "as": true, "at": true, "is": true, "be": true,
}

// stem folds the plural and possessive forms a paraphrase tends to change.
func stem(w string) string {
	switch {
	case strings.HasSuffix(w, "ies") && len(w) > 4:
		return w[:len(w)-3] + "y"
	case strings.HasSuffix(w, "ses") && len(w) > 4:
		return w[:len(w)-2]
	case strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") && len(w) > 3:
		return w[:len(w)-1]
	}
	return w
}

// overlap counts how many of want appear in have.
func overlap(want, have []string) int {
	set := make(map[string]bool, len(have))
	for _, w := range have {
		set[w] = true
	}
	n := 0
	for _, w := range want {
		if set[w] {
			n++
		}
	}
	return n
}
