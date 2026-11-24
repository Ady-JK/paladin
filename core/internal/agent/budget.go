// budget.go enforces the optional per-session spend limit. Before each
// round the session's spend (usage.json) is compared to the limit; once
// it is reached the agent takes away the tools, asks the model for a
// done/remaining summary, and ends the turn after that one reply.

package agent

import (
	"fmt"
	"strings"

	"github.com/Ady-JK/paladin/core/internal/custom"
	"github.com/Ady-JK/paladin/core/internal/inference"
	"github.com/Ady-JK/paladin/core/internal/session"
)

// Budget notice states.
const (
	BudgetReached     = "reached"
	BudgetUnknownCost = "unknownCost"
	// BudgetSpendIncomplete: the session has missing cost data, so the
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

const budgetWrapUpPrompt = "[budget] This session has used its spending budget (${{spent}} of ${{limit}}), so your tools are now disabled. " +
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
// when the ledger is corrupt or has unpriced calls, so spent is a lower bound.
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
		// SpendUsd, not Cost: a subscription plan charges $0, so a
		// budget over one would never trip if it counted the charge.
		st.spent += totals.SpendUsd()
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
		if st.incomplete && !a.spendWarned && (st.known || !a.costWarned) {
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
	// With no tools to call, the exchange so far is plain text.
	a.messages = flattenToolMessages(a.messages)
	a.messages = append(a.messages, inference.LlmMessage{Role: "user", Content: custom.TextFill("reminder.budget",
		map[string]string{"spent": fmt.Sprintf("%.2f", spent), "limit": fmt.Sprintf("%.2f", limit)})})
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

// flattenToolMessages rewrites tool calls and their results as ordinary text
// messages, leaving the input untouched. Providers refuse a request that
// carries tool blocks but defines no tools.
func flattenToolMessages(in []inference.LlmMessage) []inference.LlmMessage {
	const clip = 4000
	out := make([]inference.LlmMessage, 0, len(in))
	for _, m := range in {
		switch {
		case m.Role == "assistant" && len(m.ToolCalls) > 0:
			var b strings.Builder
			b.WriteString(m.Content)
			for _, call := range m.ToolCalls {
				if b.Len() > 0 {
					b.WriteString("\n")
				}
				args := call.Arguments
				if len(args) > 500 {
					args = args[:500] + "…"
				}
				fmt.Fprintf(&b, "[called %s %s]", call.Name, args)
			}
			m.Content, m.ToolCalls = b.String(), nil
			out = append(out, m)
		case m.Role == "tool":
			name := ""
			if m.Name != nil {
				name = *m.Name
			}
			content := m.Content
			if len(content) > clip {
				content = content[:clip] + "…"
			}
			out = append(out, inference.LlmMessage{Role: "user", Content: fmt.Sprintf("[result of %s]\n%s", name, content)})
		default:
			out = append(out, m)
		}
	}
	return out
}
