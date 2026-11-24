package runtime

import (
	"errors"
	"fmt"
	"os"

	"github.com/Ady-JK/paladin/core/internal/session"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

// A CLI budget is startup-only. New bounded user turns restart/resume the CLI
// with the remaining session budget, never a fresh allowance on top of spend.
func (r *AgentRuntime) claudeBudget(sess transcript.Session) (float64, error) {
	limit := r.turnBudget(sess.Meta.Cwd, sess.Meta.SessionID)()
	if limit <= 0 {
		return 0, nil
	}
	ledger, err := session.ReadLedger(sess.UsagePath())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return 0, err
	}
	remaining := limit - ledger.SpendUsd()
	if remaining <= 0 {
		return 0, fmt.Errorf("claude session budget of %.2f USD has been reached", limit)
	}
	return remaining, nil
}
