// budget.go stores the spend limits: an app-wide default in
// settings.json and an optional per-session override in session meta.
// The session's own value wins; 0 there turns the limit off.

package runtime

import (
	"fmt"
	"math"
	"strings"

	"github.com/Ady-JK/paladin/core/internal/transcript"
)

// Budget scopes.
const (
	BudgetScopeGlobal  = "global"
	BudgetScopeSession = "session"
)

// BudgetView is a session's spend limit and what it has spent.
type BudgetView struct {
	DefaultUsd float64 `json:"defaultUsd"`
	// SessionUsd is the session's own limit; nil inherits DefaultUsd.
	SessionUsd   *float64 `json:"sessionUsd"`
	EffectiveUsd float64  `json:"effectiveUsd"`
	SpentUsd     float64  `json:"spentUsd"`
	// HasCost is false while the provider has never reported a cost,
	// in which case the limit cannot be enforced.
	HasCost bool `json:"hasCost"`
	// Calls counts model calls so far; with HasCost false it tells
	// "nothing spent yet" apart from "the provider reports no cost".
	Calls int64 `json:"calls"`
}

// BudgetGet returns the limits that apply to a session; with no
// session it reports just the app default.
func (r *AgentRuntime) BudgetGet(cwd, sessionID string) (BudgetView, error) {
	v := BudgetView{DefaultUsd: r.loadSettings().DefaultBudgetUsd}
	v.EffectiveUsd = v.DefaultUsd
	if strings.TrimSpace(sessionID) == "" {
		return v, nil
	}
	sess, err := r.transcripts.Load(cwd, sessionID)
	if err != nil {
		return BudgetView{}, err
	}
	if b := sess.Meta.BudgetUsd; b != nil {
		v.SessionUsd = b
		v.EffectiveUsd = *b
	}
	totals, _ := readUsage(sess)
	v.SpentUsd, v.HasCost, v.Calls = totals.Cost, totals.HasCost, totals.Calls
	return v, nil
}

// BudgetSet stores a limit in USD globally or for one session; nil
// clears it (a session then inherits the default).
func (r *AgentRuntime) BudgetSet(scope, cwd, sessionID string, value *float64) (BudgetView, error) {
	if value != nil && (*value < 0 || math.IsNaN(*value) || math.IsInf(*value, 0)) {
		return BudgetView{}, fmt.Errorf("budget must be a non-negative amount, got %v", *value)
	}
	switch scope {
	case BudgetScopeGlobal:
		if err := r.setDefaultBudget(value); err != nil {
			return BudgetView{}, err
		}
	case BudgetScopeSession:
		if strings.TrimSpace(sessionID) == "" {
			return BudgetView{}, fmt.Errorf("a session budget needs a sessionId")
		}
		err := r.transcripts.UpdateMeta(cwd, sessionID, func(m *transcript.Meta) error {
			m.BudgetUsd = value
			return nil
		})
		if err != nil {
			return BudgetView{}, err
		}
	default:
		return BudgetView{}, fmt.Errorf("scope must be %q or %q, got %q", BudgetScopeGlobal, BudgetScopeSession, scope)
	}
	return r.BudgetGet(cwd, sessionID)
}

// turnBudget is the agent's live view of the limit: read before every
// round, so a change made mid-turn applies at once.
func (r *AgentRuntime) turnBudget(cwd, sessionID string) func() float64 {
	return func() float64 {
		v, err := r.BudgetGet(cwd, sessionID)
		if err != nil {
			return 0
		}
		return v.EffectiveUsd
	}
}

// setDefaultBudget stores the app-wide limit; nil or 0 clears it.
func (r *AgentRuntime) setDefaultBudget(value *float64) error {
	return r.updateSettings(func(s *Settings) {
		s.DefaultBudgetUsd = 0
		if value != nil {
			s.DefaultBudgetUsd = *value
		}
	})
}
