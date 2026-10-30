// Token and cost accounting for one session and across sessions.

package runtime

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Ady-JK/paladin/core/internal/inference"
	"github.com/Ady-JK/paladin/core/internal/session"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

// SessionUsage returns the session's billed footprint: accumulated
// tokens, gateway-reported spend, the model's context window and the
// fill percentage. Sessions that predate usage tracking (or whose
// provider never reported usage) come back with zero totals; the window
// is still resolved so the UI can show the budget.
func (r *AgentRuntime) SessionUsage(sessionID, cwd string) (session.SessionUsage, error) {
	if strings.TrimSpace(sessionID) == "" {
		return session.SessionUsage{}, fmt.Errorf("session id is required")
	}
	sess, err := r.transcripts.Load(cwd, sessionID)
	if err != nil {
		return session.SessionUsage{}, err
	}
	totals, model := readUsage(sess)
	window, known := inference.ContextWindow(model)
	return totals.View(sess.Meta.SessionID, window, known, inference.ContextWindow), nil
}

// readUsage loads a session's usage.json (zero totals when missing)
// and the model it is billed under: the usage file's, else the
// session's.
func readUsage(sess transcript.Session) (session.UsageTotals, string) {
	// A corrupt ledger shows as zero here; the budget check treats it
	// as incomplete spend instead.
	totals, _ := session.ReadLedger(sess.UsagePath())
	model := totals.Model
	if model == "" && sess.Meta.Model != nil {
		model = *sess.Meta.Model
	}
	return totals, model
}

// OverallUsage rolls every session's usage.json into one fleet-wide
// view: totals plus per-model, per-session and per-day breakdowns.
// cwd scopes like ListSessions (empty means every workspace).
// limit caps the per-session table (0 means all); totals always cover
// every session found.
func (r *AgentRuntime) OverallUsage(cwd string, limit int) (session.OverallUsage, error) {
	infos, err := r.transcripts.List(cwd, 0)
	if err != nil {
		return session.OverallUsage{}, err
	}
	out := session.OverallUsage{SessionCount: int64(len(infos))}
	byModel := map[string]*session.ModelUsageView{}
	byDay := map[string]*session.Counters{}
	addModel := func(name string, c session.Counters) {
		agg := byModel[name]
		if agg == nil {
			agg = &session.ModelUsageView{Model: name}
			byModel[name] = agg
		}
		agg.Add(c)
	}
	for _, info := range infos {
		sess, err := r.transcripts.Load(info.Meta.Cwd, info.Meta.SessionID)
		if err != nil {
			continue
		}
		totals, model := readUsage(sess)
		if totals.Calls == 0 && totals.TotalTokens == 0 {
			continue
		}
		out.SessionsWithUsage++
		out.Add(totals.Counters)
		out.UpdatedAt = max(out.UpdatedAt, totals.UpdatedAt)
		out.Sessions = append(out.Sessions, session.OverallSessionEntry{
			SessionID: sess.Meta.SessionID,
			Title:     info.Title,
			Model:     model,
			CreatedAt: sess.Meta.CreatedAt.Unix(),
			UpdatedAt: totals.UpdatedAt,
			Counters:  totals.Counters,
		})
		for name, m := range totals.Models {
			if m != nil {
				addModel(name, m.Counters)
			}
		}
		// Sessions that predate per-model books still contribute to
		// the model share under their session model.
		if len(totals.Models) == 0 && model != "" {
			addModel(model, totals.Counters)
		}
		for date, d := range totals.Daily {
			if d == nil {
				continue
			}
			agg := byDay[date]
			if agg == nil {
				agg = &session.Counters{}
				byDay[date] = agg
			}
			agg.Add(*d)
		}
	}
	for _, m := range byModel {
		out.Models = append(out.Models, *m)
	}
	session.SortModelsHeaviestFirst(out.Models)
	sort.Slice(out.Sessions, func(i, j int) bool {
		if out.Sessions[i].TotalTokens != out.Sessions[j].TotalTokens {
			return out.Sessions[i].TotalTokens > out.Sessions[j].TotalTokens
		}
		return out.Sessions[i].SessionID < out.Sessions[j].SessionID
	})
	if limit > 0 && len(out.Sessions) > limit {
		out.Sessions = out.Sessions[:limit]
	}
	out.Daily = session.DailyPoints(byDay)
	return out, nil
}
