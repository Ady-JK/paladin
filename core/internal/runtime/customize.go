// customize.go is the runtime side of Settings → Customize: it registers
// the runtime's own texts, the shipped commands and skills, and the
// settings files, and serves the editor's RPCs. The texts themselves
// live in internal/custom.

package runtime

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/Ady-JK/paladin/core/internal/commands"
	"github.com/Ady-JK/paladin/core/internal/custom"
	"github.com/Ady-JK/paladin/core/internal/lsp"
	"github.com/Ady-JK/paladin/core/internal/memory"
	"github.com/Ady-JK/paladin/core/internal/skills"
	"github.com/Ady-JK/paladin/core/internal/tools"
)

func (r *AgentRuntime) registerCustomizable() {
	custom.SetDataDir(r.dataDir)
	defs := r.tools.Definitions()
	if rf, ok := tools.CommandTool("report_findings"); ok {
		defs = append(defs, rf.Definition())
	}
	tools.RegisterDescriptions(defs)
	custom.Register(
		custom.Item{ID: "subagent.preamble", Group: "Subagents", Title: "Every subagent", Order: 0,
			Description: "Starts every subagent's role: how a child agent works and reports back.",
			Default:     func() string { return subagentPreamble }},
		custom.Item{ID: "section.tools-off", Group: "Reminders", Title: "Tools switched off", Order: 20,
			Description: "Added to the system prompt when you switch tools off, so the agent ignores their guidance.",
			Vars:        []string{"tools"}, Default: func() string { return toolsOffDefault }},
		custom.Item{ID: "side.question", Group: "Modes", Title: "Side question (/btw)", Order: 5,
			Description: "Wraps a /btw question so the model answers it briefly without resuming the task.",
			Vars:        []string{"question"}, Default: func() string { return sideQuestionDefault }},
		custom.Item{ID: "file.settings", Group: "Settings files", Title: "settings.json", Order: 0, Kind: custom.KindJSON,
			Description: "App settings: web limits (web.timeoutMs, maxBytes, maxChars), maxConcurrentTurns, maxSubagents, defaultBudgetUsd, disabledSkills, behavior switches. Reset deletes the file, so every setting returns to its default.",
			File:        "settings.json", Default: func() string { return "{}\n" }, Validate: validateSettings},
		custom.Item{ID: "file.lsp", Group: "Settings files", Title: "lsp.json", Order: 1, Kind: custom.KindJSON,
			Description: "Language servers: timeoutMs and per-server command, extensions, rootMarkers, env or disabled.",
			File:        "lsp.json", Default: func() string { return "{\n  \"timeoutMs\": 1500,\n  \"servers\": {}\n}\n" },
			Validate: lsp.ValidateConfig},
		custom.Item{ID: "file.memory", Group: "Settings files", Title: "memory.json", Order: 2, Kind: custom.KindJSON,
			Description: "Persistent memory: per-target char budgets, which scopes are switched on, the backend provider, and how many turns may pass without a write before a review is suggested. Reset deletes the file, so every setting returns to its default.",
			File:        "memory.json", Default: memory.DefaultConfigText, Validate: memory.ValidateConfig},
	)
	for i, k := range subagentKinds {
		role := k.Role
		custom.Register(custom.Item{ID: "subagent." + k.Name, Group: "Subagents", Title: k.Name + " role", Order: 1 + i,
			Description: k.Description, Default: func() string { return role }})
	}
	for i, name := range commands.BuiltinNames() {
		text, _ := commands.BuiltinText(name)
		custom.Register(custom.Item{ID: "command." + name, Group: "Commands", Title: "/" + name, Order: i,
			Kind: custom.KindCommand, File: "commands/" + name + ".md",
			Description: "The /" + name + " prompt. Frontmatter sets description, argument-hint, mode and tools; $ARGUMENTS is what you type after it.",
			Default:     func() string { return text }})
	}
	for i, s := range skills.Builtins() {
		text := s.Content
		custom.Register(custom.Item{ID: "skill." + s.Name, Group: "Skills", Title: s.Name, Order: i,
			Kind: custom.KindSkill, File: "skills/" + s.Name + "/SKILL.md", Description: s.Description,
			Default: func() string { return text }})
	}
}

// validateSettings accepts settings.json text the runtime can read.
func validateSettings(text string) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &raw); err != nil || raw == nil {
		return errors.New("settings.json must be a JSON object")
	}
	var s Settings
	dec := json.NewDecoder(bytes.NewReader([]byte(text)))
	if err := dec.Decode(&s); err != nil {
		return errors.New("settings.json: " + err.Error())
	}
	return nil
}

// CustomItem is one row of the editor's list.
type CustomItem struct {
	custom.Item
	Customized bool `json:"customized"`
	// Enabled is set for skills (false when turned off).
	Enabled *bool `json:"enabled,omitempty"`
}

// CustomList lists every editable built-in.
func (r *AgentRuntime) CustomList() []CustomItem {
	off := skills.Disabled(r.dataDir)
	var out []CustomItem
	for _, it := range custom.Items() {
		st, err := custom.Get(it.ID)
		if err != nil {
			continue
		}
		row := CustomItem{Item: it, Customized: st.Customized}
		if it.Kind == custom.KindSkill {
			on := !off[strings.ToLower(strings.TrimPrefix(it.ID, "skill."))]
			row.Enabled = &on
		}
		out = append(out, row)
	}
	return out
}

// CustomState is one item with its text, for the editor.
type CustomState struct {
	custom.State
	// Missing are placeholders the current text no longer uses.
	Missing []string `json:"missing,omitempty"`
	Enabled *bool    `json:"enabled,omitempty"`
}

func (r *AgentRuntime) customState(st custom.State) CustomState {
	out := CustomState{State: st, Missing: custom.Missing(st.Item, st.Value)}
	if st.Kind == custom.KindSkill {
		on := !skills.Disabled(r.dataDir)[strings.ToLower(strings.TrimPrefix(st.ID, "skill."))]
		out.Enabled = &on
	}
	return out
}

// CustomGet returns one item.
func (r *AgentRuntime) CustomGet(id string) (CustomState, error) {
	st, err := custom.Get(id)
	if err != nil {
		return CustomState{}, err
	}
	return r.customState(st), nil
}

// CustomSet saves an edit. settings.json goes through the settings lock
// so it cannot race a switch being flipped.
func (r *AgentRuntime) CustomSet(id, text string) (CustomState, error) {
	if id == "file.settings" {
		r.settingsMu.Lock()
		defer r.settingsMu.Unlock()
	}
	st, err := custom.Set(id, text)
	if err != nil {
		return CustomState{}, err
	}
	return r.customState(st), nil
}

// CustomReset restores an item's default.
func (r *AgentRuntime) CustomReset(id string) (CustomState, error) {
	if id == "file.settings" {
		r.settingsMu.Lock()
		defer r.settingsMu.Unlock()
	}
	st, err := custom.Reset(id)
	if err != nil {
		return CustomState{}, err
	}
	return r.customState(st), nil
}

// SkillEnabledSet turns a skill on or off by name.
func (r *AgentRuntime) SkillEnabledSet(name string, on bool) error {
	name = strings.TrimSpace(name)
	if !skills.ValidName(name) {
		return errors.New("invalid skill name")
	}
	return r.updateSettings(func(s *Settings) {
		s.DisabledSkills = slices.DeleteFunc(s.DisabledSkills, func(n string) bool { return strings.EqualFold(n, name) })
		if !on {
			s.DisabledSkills = append(s.DisabledSkills, name)
		}
	})
}
