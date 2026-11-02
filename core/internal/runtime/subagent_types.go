// subagent_types.go defines the built-in subagent kinds: their role
// prompt and the most tools they may ever use. A child's real tool set
// is this list narrowed by what its parent has switched on.

package runtime

import (
	"slices"
	"sort"
	"strings"

	"github.com/Ady-JK/paladin/core/internal/agent"
	"github.com/Ady-JK/paladin/core/internal/custom"
)

// subagentKind is one spawnable agent type.
type subagentKind struct {
	Name        string
	Description string
	// Tools is the allowlist before the parent's switches apply.
	Tools []string
	// Role follows the shared preamble in the child's prompt; both are
	// editable (subagent.preamble, subagent.<name>).
	Role string
}

const subagentPreamble = `# SUBAGENT
You are a subagent: another agent (your parent) gave you one focused task.
- Nobody reads your messages while you work. Your final reply is the
  only thing returned to the parent, so make it self-contained: lead with
  the answer, then the evidence (file paths with line numbers, commands
  and their key output). Keep it under about 400 words.
- You are read-only: you cannot write or edit files and the shell only
  runs inspection commands. The workspace is shared with the parent and
  other subagents, so files may change while you read them.
- Work only on the task you were given; do not ask the user questions.
  If the task is unclear or blocked, say exactly what is missing.
`

var subagentKinds = []subagentKind{
	{
		Name:        "explore",
		Description: "Fast read-only codebase exploration: find files, trace call paths, answer questions about how code works.",
		Tools:       []string{"read", "grep", "glob", "list", "shell"},
		Role: `## Role: explore
Investigate the codebase to answer the parent's question. Search likely
locations first, widening only when needed, then read the relevant code.
Report concrete findings: where things live, how they connect, and anything surprising.
Do not propose large redesigns unless asked.`,
	},
	{
		// The memory review fork: it may read to check a fact, but the
		// only thing it can change is memory. It never edits the
		// workspace, so a review can never race the user's own edits.
		Name:        "memory-review",
		Description: "Review a conversation for durable facts and save them to memory. Memory writes only: it cannot change the workspace.",
		Tools:       []string{"memory", "read", "grep", "glob", "list"},
		Role: `## Role: memory review
The conversation above is finished. Decide what, if any, durable facts it
produced, then save them with the memory tool.

You cannot change the workspace: no write, no edit, no shell. Reading is
allowed to confirm a fact you are about to save (the version in go.mod,
the command in the Makefile) — do it instead of guessing.

Prefer quality over volume. One precise entry beats five vague ones. Patch
an existing entry that is now wrong or incomplete rather than adding a
near-duplicate. Save nothing when nothing clears the bar.`,
	},
	{
		Name:        "reviewer",
		Description: "Read-only code review: inspect a diff or files for bugs, regressions, missing tests and risky changes.",
		Tools:       []string{"read", "grep", "glob", "list", "shell"},
		Role: `## Role: reviewer
Review the code or change the parent describes (use git diff / git show
when it names a change). Report only real problems: bugs, logic errors,
regressions, security issues, missing error handling or tests. For each,
give the file and line, why it is wrong, and a suggested fix. Skip style
nits. If you find nothing significant, say so plainly.`,
	},
}

// reviewKind is the memory review fork's kind. Looked up by name so the
// runtime and the customize pane agree on one definition.
func reviewKind() subagentKind {
	k, ok := findSubagentKind("memory-review")
	if !ok {
		return subagentKind{Name: "memory-review", Tools: []string{"memory"}}
	}
	return k
}

// hiddenSubagentKinds are spawned by the runtime, not offered to the
// model: they would only waste a delegation on work the runtime starts
// itself.
var hiddenSubagentKinds = []string{"memory-review"}

// subagentKindVisible reports whether the model may spawn a kind.
func subagentKindVisible(name string) bool {
	return !slices.Contains(hiddenSubagentKinds, name)
}

func findSubagentKind(name string) (subagentKind, bool) {
	for _, k := range subagentKinds {
		if k.Name == name {
			return k, true
		}
	}
	return subagentKind{}, false
}

func subagentKindNames() []string {
	names := make([]string, 0, len(subagentKinds))
	for _, k := range subagentKinds {
		if subagentKindVisible(k.Name) {
			names = append(names, k.Name)
		}
	}
	sort.Strings(names)
	return names
}

// subagentTemplate is the agent template a child of this kind runs.
// Tools stays nil: the runtime pre-filters the registry, because the
// parent may have switched some of the allowlist off.
func (k subagentKind) template() agent.Template {
	return agent.Template{Name: "subagent:" + k.Name, ReadOnly: true, Prompt: k.prompt()}
}

// prompt is the preamble plus the kind's role, as the user has them.
func (k subagentKind) prompt() string {
	return strings.TrimRight(custom.Text("subagent.preamble"), "\n") + "\n\n" + strings.TrimSpace(custom.Text("subagent."+k.Name))
}

func (k subagentKind) allows(tool string) bool { return slices.Contains(k.Tools, tool) }
