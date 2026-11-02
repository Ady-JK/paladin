// plan.go is the unfinished-plan nudge. When a turn is about to end
// while the session's activity list still has open steps (a step left
// in_progress, or never started), the model gets a short reminder
// naming them: mark each finished (or cancelled) if the work is done,
// else keep working. It fires at most maxPlanNudges times per turn so
// a model that refuses to touch the list still ends the turn instead
// of looping forever. User-stopped turns never nudge: every stop path
// returns before finishTextRound runs.

package agent

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Ady-JK/paladin/core/internal/custom"
	"github.com/Ady-JK/paladin/core/internal/inference"
	"github.com/Ady-JK/paladin/core/internal/session"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

const planDisplay = "Finish the plan"

const planDefault = "Before you finish: the activity list still has unfinished steps:\n{{steps}}\n" +
	"If that work is actually done, mark each step finished (or cancelled) with the todo tool now. " +
	"Otherwise keep working through the remaining steps — do not end the turn with steps left in_progress or not_started."

// planMaxSteps caps the steps named in the reminder.
// maxPlanNudges caps reminders per turn; a model that will not settle
// its list still ends the turn after this many extra rounds.
const (
	planMaxSteps  = 8
	maxPlanNudges = 3
)

// planNudge reminds the model about open plan steps before the turn
// ends. It reports whether the turn goes on.
func (a *Agent) planNudge() bool {
	if len(a.toolDefs) == 0 || a.planNudges >= maxPlanNudges || a.halt.tripped() {
		return false
	}
	// No list, an empty list or an unreadable one means no plan to
	// enforce: fail open, never fail (or loop) a turn over storage.
	if strings.TrimSpace(a.session.Dir) == "" {
		return false
	}
	data, err := os.ReadFile(filepath.Join(a.session.Dir, session.TodoFileName))
	if err != nil {
		return false
	}
	var open []string
	for _, td := range session.TodosView(data) {
		if planStepOpen(td.Status) {
			open = append(open, strings.TrimSpace(td.Content)+" ("+strings.TrimSpace(td.Status)+")")
		}
	}
	if len(open) == 0 {
		return false
	}
	a.planNudges++
	steps := open
	more := ""
	if len(steps) > planMaxSteps {
		more = " and " + strconv.Itoa(len(steps)-planMaxSteps) + " more"
		steps = steps[:planMaxSteps]
	}
	content := custom.TextFill("reminder.plan", map[string]string{"steps": strings.Join(steps, "\n") + more})
	a.messages = append(a.messages, inference.LlmMessage{Role: "user", Content: content})
	rec := transcript.UserMessage(content)
	rec.Display = planDisplay
	a.records = append(a.records, rec)
	return true
}

// planStepOpen reports whether a stored status still needs work. It
// mirrors TodosFinished (finished/cancelled land the step) and also
// accepts the legacy names the todo tool renames on write.
func planStepOpen(status string) bool {
	switch strings.TrimSpace(status) {
	case "finished", "cancelled", "completed":
		return false
	default:
		return true
	}
}
