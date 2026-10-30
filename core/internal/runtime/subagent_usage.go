// subagent_usage.go attributes a child's spending to its parent: the
// root session and its children share one budget.

package runtime

import (
	"github.com/Ady-JK/paladin/core/internal/session"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

// family is the root session and its children: the budget shares one
// limit across them.
func (m *subagentManager) family(sess transcript.Session) []transcript.Session {
	root := sess
	if sa := sess.Meta.Subagent; sa != nil {
		if p, err := m.load(sess.Meta.Cwd, sa.ParentID); err == nil {
			root = p
		}
	}
	return append([]transcript.Session{root}, m.children(root)...)
}

// spendLedgers lists the usage ledgers a session's budget counts.
func (m *subagentManager) spendLedgers(cwd, sessionID string) func() []string {
	return func() []string {
		sess, err := m.load(cwd, sessionID)
		if err != nil {
			return nil
		}
		members := m.family(sess)
		paths := make([]string, 0, len(members))
		for _, s := range members {
			paths = append(paths, s.UsagePath())
		}
		return paths
	}
}

// familyUsage sums the ledgers of a session's family.
func (m *subagentManager) familyUsage(sess transcript.Session) session.UsageTotals {
	members := m.family(sess)
	var total session.UsageTotals
	for _, s := range members {
		t, _ := readUsage(s)
		total.Cost += t.Cost
		total.HasCost = total.HasCost || t.HasCost
		total.Calls += t.Calls
	}
	return total
}
