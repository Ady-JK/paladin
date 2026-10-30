// subagent_types.go defines the built-in subagent kinds: their role
// prompt and the most tools they may ever use. A child's real tool set
// is this list narrowed by what its parent has switched on.

package runtime

import (
	"slices"
	"sort"

	"github.com/Ady-JK/paladin/core/internal/agent"
)

// subagentKind is one spawnable agent type.
type subagentKind struct {
	Name        string
	Description string
	// Tools is the allowlist before the parent's switches apply.
	Tools  []string
	Prompt string
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
		Tools:       []string{"read", "shell"},
		Prompt: subagentPreamble + `
## Role: explore
Investigate the codebase to answer the parent's question. Search broadly
first (rg, ls, git log), then read the relevant code. Report concrete
findings: where things live, how they connect, and anything surprising.
Do not propose large redesigns unless asked.`,
	},
	{
		Name:        "reviewer",
		Description: "Read-only code review: inspect a diff or files for bugs, regressions, missing tests and risky changes.",
		Tools:       []string{"read", "shell"},
		Prompt: subagentPreamble + `
## Role: reviewer
Review the code or change the parent describes (use git diff / git show
when it names a change). Report only real problems: bugs, logic errors,
regressions, security issues, missing error handling or tests. For each,
give the file and line, why it is wrong, and a suggested fix. Skip style
nits. If you find nothing significant, say so plainly.`,
	},
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
		names = append(names, k.Name)
	}
	sort.Strings(names)
	return names
}

// subagentTemplate is the agent template a child of this kind runs.
// Tools stays nil: the runtime pre-filters the registry, because the
// parent may have switched some of the allowlist off.
func (k subagentKind) template() agent.Template {
	return agent.Template{Name: "subagent:" + k.Name, ReadOnly: true, Prompt: k.Prompt}
}

func (k subagentKind) allows(tool string) bool { return slices.Contains(k.Tools, tool) }
