package agent

import "github.com/Ady-JK/paladin/core/internal/custom"

// The agent loop's own texts, editable in Settings → Customize.
func init() {
	custom.Register(
		custom.Item{ID: "reminder.budget", Group: "Reminders", Title: "Budget reached", Order: 5,
			Description: "Sent when a session reaches its spending budget: tools are switched off and the agent sums up.",
			Vars:        []string{"spent", "limit"}, Default: func() string { return budgetWrapUpPrompt }},
		custom.Item{ID: "reminder.plan", Group: "Reminders", Title: "Unfinished plan", Order: 2,
			Description: "Sent when a turn is about to end while plan steps are still open.",
			Vars:        []string{"steps"}, Default: func() string { return planDefault }},
		custom.Item{ID: "compact.system", Group: "Compaction", Title: "Summarizer instructions", Order: 0,
			Description: "The system prompt for summarizing a long conversation when it outgrows the context window.",
			Default:     func() string { return compactSystemPrompt }},
		custom.Item{ID: "compact.summary", Group: "Compaction", Title: "Summary hand-over", Order: 1,
			Description: "How the summary is handed to the agent in place of the earlier conversation.",
			Vars:        []string{"summary"}, Default: func() string { return compactSummaryDefault }},
	)
}
