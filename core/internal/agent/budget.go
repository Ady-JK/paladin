// budget.go enforces the optional per-session spend limit. Before each
// round the session's spend (usage.json) is compared to the limit; once
// it is reached the agent takes away the tools, asks the model for a
// done/remaining summary, and ends the turn after that one reply.

package agent

import (
	"fmt"

	"github.com/Ady-JK/paladin/core/internal/inference"
	"github.com/Ady-JK/paladin/core/internal/session"
)

// Budget notice states.
const (
	BudgetReached     = "reached"
	BudgetUnknownCost = "unknownCost"
	// BudgetSpendIncomplete: the session's ledger was corrupt, so the
	// spend shown is only a lower bound. The limit is still enforced
	// against it.
	BudgetSpendIncomplete = "spendIncomplete"
)

// BudgetNotice is the spend limit's state as of a check.
type BudgetNotice struct {
	State string  `json:"state"`
	Spent float64 `json:"spent"`
	Limit float64 `json:"limit"`
	// Ended: the limit was already spent when the turn began, so it
	// ended without a model call (no wrap-up summary follows).
	Ended bool `json:"ended,omitempty"`
}

const budgetWrapUpPrompt = "[budget] This session has used its spending budget ($%.2f of $%.2f), so your tools are now disabled. " +
	"Do not call any tools. Reply with a short summary: what you finished, what remains to be done, and the next steps, " +
	"so the user can decide whether to raise the budget."

func (a *Agent) budgetLimit() float64 {
	if a.budget == nil {
		return 0
	}
	return a.budget()
}

// spendState is the session's gateway-reported spend as of a check.
// known is false while no call has reported a cost; incomplete is true
// when the ledger is (or was) corrupt, so spent is only a lower bound.
type spendState struct {
	spent             float64
	known, incomplete bool
}

// sessionSpend sums every ledger the budget covers. An unreadable one
// makes the total a lower bound.
func (a *Agent) sessionSpend() spendState {
	paths := []string{a.session.UsagePath()}
	if a.spendLedgers != nil {
		paths = a.spendLedgers()
	}
	var st spendState
	for _, path := range paths {
		totals, err := session.ReadLedger(path)
		if err != nil {
			st.incomplete = true
			continue
		}
		st.spent += totals.Cost
		st.known = st.known || totals.HasCost
		st.incomplete = st.incomplete || totals.SpendIncomplete()
	}
	return st
}

// checkBudget runs before each round. It reports end=true when the turn
// must stop now: the budget was already spent before this turn made any
// call. Hitting the limit mid-turn instead switches to the wrap-up round.
func (a *Agent) checkBudget() (end bool, err error) {
	first := !a.budgetChecked
	a.budgetChecked = true
	if a.budgetWrapUp {
		return false, nil
	}
	limit := a.budgetLimit()
	if limit <= 0 {
		return false, nil
	}
	st := a.sessionSpend()
	spent := st.spent
	if !st.known || spent < limit {
		if st.incomplete && !a.spendWarned {
			a.spendWarned = true
			if err := a.emit(ChatChunk{Budget: &BudgetNotice{State: BudgetSpendIncomplete, Spent: spent, Limit: limit}}); err != nil {
				return false, turnStop{}
			}
		}
		return false, nil
	}
	if err := a.emit(ChatChunk{Budget: &BudgetNotice{State: BudgetReached, Spent: spent, Limit: limit, Ended: first}}); err != nil {
		return false, turnStop{}
	}
	if first {
		a.finish(phaseDone)
		return true, nil
	}
	a.budgetWrapUp = true
	a.toolDefs = nil
	a.messages = append(a.messages, inference.LlmMessage{Role: "user", Content: fmt.Sprintf(budgetWrapUpPrompt, spent, limit)})
	return false, nil
}

// noteCallCost warns once per turn when a budget is set but the
// provider's calls carry no cost, so the limit cannot be enforced.
func (a *Agent) noteCallCost(u inference.Usage) {
	if a.costWarned || u.HasCost {
		return
	}
	limit := a.budgetLimit()
	if limit <= 0 {
		return
	}
	a.costWarned = true
	_ = a.emit(ChatChunk{Budget: &BudgetNotice{State: BudgetUnknownCost, Spent: a.sessionSpend().spent, Limit: limit}})
}
