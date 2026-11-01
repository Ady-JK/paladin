package tools

import (
	"strings"

	"github.com/Ady-JK/paladin/core/internal/custom"
)

const (
	nestedHeaderDefault = "Instructions for this part of the project (AGENTS.md in its folders). Follow them for files there; " +
		"deeper files win when they conflict, and direct user instructions beat all of them."
	diagnosticsDefault = "{{server}} reports {{count}} errors in this file after the change{{shown}}; fix them, or say why they are expected."
)

// The notes tools add to their results, editable in Settings → Customize.
// Tool descriptions register separately (RegisterDescriptions).
func init() {
	custom.Register(
		custom.Item{ID: "note.nested-header", Group: "Reminders", Title: "Folder instructions", Order: 10,
			Description: "Heads the AGENTS.md files of a subfolder when the agent first touches a file there.",
			Default:     func() string { return nestedHeaderDefault }},
		custom.Item{ID: "note.nested-held", Group: "Reminders", Title: "Write held for folder instructions", Order: 11,
			Description: "Why the first write or edit into a folder with unseen instructions did not run yet.",
			Default:     func() string { return heldMessage }},
		custom.Item{ID: "note.formatted", Group: "Reminders", Title: "File was formatted", Order: 12,
			Description: "Added to write/edit results when the project's formatter changed the file.",
			Vars:        []string{"formatter"}, Default: func() string { return formatNote }},
		custom.Item{ID: "note.diagnostics", Group: "Reminders", Title: "Language server errors", Order: 13,
			Description: "Added to write/edit results when a language server reports errors ({{shown}} is \" (showing N)\" when capped).",
			Vars:        []string{"server", "count"}, Default: func() string { return diagnosticsDefault }},
		custom.Item{ID: "note.web-untrusted", Group: "Reminders", Title: "Web content is untrusted", Order: 14,
			Description: "Attached to every web_fetch result.",
			Default:     func() string { return webUntrusted }},
	)
}

// RegisterDescriptions makes each tool's description editable as
// tool.<name>.description, its default the tool's own.
func RegisterDescriptions(list []ToolDefinition) {
	for _, d := range list {
		desc := d.Description
		custom.Register(custom.Item{ID: "tool." + d.Name + ".description", Group: "Tool descriptions",
			Title: d.Name, Description: "What the model reads about the " + d.Name + " tool: what it does and when to use it.",
			Default: func() string { return desc }})
	}
}

// Described applies edited descriptions to tool definitions. Only the
// built-in text is swapped, so wrappers that extend it (plan mode's
// shell) keep their additions.
func Described(defs []ToolDefinition) []ToolDefinition {
	out := make([]ToolDefinition, len(defs))
	for i, d := range defs {
		if it, ok := custom.Lookup("tool." + d.Name + ".description"); ok {
			if def, now := it.Default(), custom.Text(it.ID); now != def && strings.Contains(d.Description, def) {
				d.Description = strings.Replace(d.Description, def, now, 1)
			}
		}
		out[i] = d
	}
	return out
}
