// ContextView reports what a session loads into its system prompt on
// every turn without the user or the agent asking: project instruction
// files (AGENTS.md and friends), the memory stores, the skill catalog
// and MCP server notes. The composer shows it so a folder's implicit
// context is never a mystery; each entry carries the text the model
// actually sees, so the box can preview it.
//
// It mirrors runtimeSections.SystemSections and must be kept in step
// with it: an entry here that the prompt does not carry (or the other
// way round) would mislead the user about what the model was told.

package runtime

import (
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Ady-JK/paladin/core/internal/instructions"
	"github.com/Ady-JK/paladin/core/internal/memory"
	"github.com/Ady-JK/paladin/core/internal/skills"
	"github.com/Ady-JK/paladin/core/internal/tools"
)

// ContextEntry is one auto-loaded source.
type ContextEntry struct {
	// Kind is "instructions", "memory", "skills" or "mcp".
	Kind string `json:"kind"`
	// Scope says what the source belongs to: "global", "project" or a
	// memory target name.
	Scope string `json:"scope"`
	// Title is the short row label, e.g. "AGENTS.md".
	Title string `json:"title"`
	// Path is the file behind the entry; memory stores point at their
	// file, the skill catalog at the folder it scans.
	Path string `json:"path,omitempty"`
	// Detail is the one-line explanation next to the title.
	Detail string `json:"detail,omitempty"`
	// Bytes is the size of what the prompt carries.
	Bytes int `json:"bytes"`
	// Truncated marks a source the core clipped before injecting it.
	Truncated bool `json:"truncated,omitempty"`
	// Content is the text injected into the system prompt, for preview.
	Content string `json:"content,omitempty"`
}

// ContextView is the whole box: sources in prompt order plus the total
// size of the implicit context, and the counts the summary line uses.
type ContextView struct {
	Cwd      string         `json:"cwd"`
	Sections []ContextEntry `json:"sections"`
	Bytes    int            `json:"bytes"`
	Files    int            `json:"files"`
	Skills   int            `json:"skills"`
	MCP      int            `json:"mcp"`
}

// ContextSources lists the sources the auto-loaded part of the system
// prompt for cwd consists of. It renders the same helpers the turn path
// uses, so sizes match what the model receives.
func (r *AgentRuntime) ContextSources(cwd string) ContextView {
	cwd = strings.TrimSpace(cwd)
	view := ContextView{Cwd: cwd, Sections: []ContextEntry{}}

	// Project instructions: one row per file, in prompt order.
	for _, doc := range instructions.Discover(r.dataDir, cwd) {
		view.Sections = append(view.Sections, ContextEntry{
			Kind:      "instructions",
			Scope:     doc.Scope,
			Title:     filepath.Base(doc.Path),
			Path:      doc.Path,
			Detail:    instructionDetail(doc.Scope, doc.Path),
			Bytes:     len(doc.Content),
			Truncated: doc.Truncated,
			Content:   doc.Content,
		})
	}

	if r.memory != nil {
		view.Sections = append(view.Sections, memoryContext(r.memory, cwd)...)
	}

	// The skill catalog only joins the prompt while the skill tool is on.
	if _, ok := r.tools.Get("skill"); ok {
		list := skills.List(r.dataDir, cwd)
		if text := tools.SkillCatalog(r.dataDir, cwd, skillCatalogMax); text != "" {
			view.Skills = len(list)
			detail := strconv.Itoa(len(list)) + " described; the skill tool loads one on demand"
			if len(list) > skillCatalogMax {
				detail = "First " + strconv.Itoa(skillCatalogMax) + " of " + strconv.Itoa(len(list)) + " described; the skill tool loads one on demand"
			}
			view.Sections = append(view.Sections, ContextEntry{
				Kind:    "skills",
				Scope:   "global",
				Title:   "Skill catalog",
				Path:    skills.GlobalDir(r.dataDir),
				Detail:  detail,
				Bytes:   len(text),
				Content: text,
			})
		}
	}

	if text := mcpSection(r.mcp, cwd); text != "" {
		view.MCP = strings.Count(text, "\n### MCP server ")
		if strings.HasPrefix(text, "## MCP servers\nTools named") && view.MCP == 0 {
			view.MCP = 1
		}
		detail := strconv.Itoa(view.MCP) + " publishing notes"
		if view.MCP == 1 {
			detail = "1 server publishes notes"
		}
		view.Sections = append(view.Sections, ContextEntry{
			Kind:    "mcp",
			Scope:   "global",
			Title:   "MCP server notes",
			Detail:  detail,
			Bytes:   len(text),
			Content: text,
		})
	}

	for _, entry := range view.Sections {
		view.Bytes += entry.Bytes
		if entry.Kind == "instructions" {
			view.Files++
		}
	}
	return view
}

// memoryContext renders one row per enabled memory scope that has
// entries — the same scopes and order memory.RenderSections uses.
func memoryContext(bank *memory.Bank, cwd string) []ContextEntry {
	var out []ContextEntry
	for _, target := range []memory.Target{memory.TargetUser, memory.TargetGlobal, memory.TargetProject} {
		if target == memory.TargetProject && cwd == "" {
			continue
		}
		if !bank.Enabled(target) {
			continue
		}
		res, err := bank.Read(target, cwd)
		if err != nil || len(res.Entries) == 0 {
			continue
		}
		store, err := bank.Store(target, cwd)
		if err != nil {
			continue
		}
		out = append(out, ContextEntry{
			Kind:  "memory",
			Scope: string(target),
			Title: memoryTitle(target),
			Path:  store.Path(),
			Detail: strconv.Itoa(len(res.Entries)) + " " + plural(len(res.Entries), "entry") +
				" · " + strconv.Itoa(res.Used) + "/" + strconv.Itoa(res.Limit) + " chars",
			Bytes:   res.Used,
			Content: strings.Join(res.Entries, memory.Delim),
		})
	}
	return out
}

func memoryTitle(t memory.Target) string {
	switch t {
	case memory.TargetUser:
		return "User profile memory"
	case memory.TargetGlobal:
		return "Global memory"
	default:
		return "Folder memory"
	}
}

func instructionDetail(scope, path string) string {
	if scope == "global" {
		return "Your global instructions, added to every session"
	}
	dir := filepath.Dir(path)
	if name := filepath.Base(path); name == "AGENTS.override.md" {
		return "Project instructions for " + dir + " (override)"
	}
	return "Project instructions for " + dir
}

func plural(n int, one string) string {
	if n == 1 {
		return one
	}
	return one + "ies"
}
