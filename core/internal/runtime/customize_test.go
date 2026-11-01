package runtime

import (
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/custom"
	"github.com/Ady-JK/paladin/core/internal/prompt"
	"github.com/Ady-JK/paladin/core/internal/skills"
	"github.com/Ady-JK/paladin/core/internal/tools"
)

func TestCustomizeListsEveryKindOfBuiltin(t *testing.T) {
	rt, _ := testRuntime(t)
	ids := map[string]CustomItem{}
	for _, it := range rt.CustomList() {
		ids[it.ID] = it
	}
	for _, id := range []string{"prompt.system", "prompt.tool-guidance", "prompt.shell-guidance", "prompt.examples",
		"prompt.plan-mode", "tool.read.description", "tool.web_fetch.description", "tool.visual.description",
		"tool.spawn_agent.description", "tool.report_findings.description", "reminder.verify", "reminder.budget",
		"note.nested-held", "subagent.preamble", "subagent.explore", "compact.system", "side.question",
		"section.tools-off", "command.review", "command.init", "skill.debugging", "file.settings", "file.lsp"} {
		if _, ok := ids[id]; !ok {
			t.Errorf("missing %s", id)
		}
	}
	if it := ids["skill.debugging"]; it.Enabled == nil || !*it.Enabled || it.Kind != custom.KindSkill {
		t.Fatalf("skill row %+v", it)
	}
	if ids["reminder.verify"].Vars[0] != "files" {
		t.Fatal("placeholders are listed")
	}
}

func TestCustomizedTextReachesTheAgent(t *testing.T) {
	rt, ws := testRuntime(t)
	// A tool description, also inside plan mode's shell wrapper.
	if _, err := rt.CustomSet("tool.shell.description", "Run commands. Prefer make targets."); err != nil {
		t.Fatal(err)
	}
	defs := tools.Described([]tools.ToolDefinition{tools.NewShellTool().Definition(), tools.NewPlanShell(tools.NewShellTool()).Definition()})
	if defs[0].Description != "Run commands. Prefer make targets." || !strings.HasPrefix(defs[1].Description, "PLAN MODE") ||
		!strings.Contains(defs[1].Description, "Prefer make targets.") {
		t.Fatalf("descriptions %q / %q", defs[0].Description, defs[1].Description)
	}
	// Tool guidance lands in the system prompt.
	if _, err := rt.CustomSet("prompt.tool-guidance", "Tools:\n- Always grep before reading."); err != nil {
		t.Fatal(err)
	}
	sys := prompt.ToolSystem("BASE", ws)
	if !strings.Contains(sys, "Always grep before reading.") || strings.Contains(sys, "Read a file before you describe") {
		t.Fatalf("guidance not replaced: %.300s", sys)
	}
	// A subagent role.
	if _, err := rt.CustomSet("subagent.explore", "## Role: explore\nOnly list files."); err != nil {
		t.Fatal(err)
	}
	if p := subagentKinds[0].prompt(); !strings.HasPrefix(p, "# SUBAGENT") || !strings.HasSuffix(p, "Only list files.") {
		t.Fatalf("subagent prompt %q", p)
	}
	// A built-in command.
	if _, err := rt.CustomSet("command.review", "---\ndescription: Mine\n---\nReview gently: $ARGUMENTS"); err != nil {
		t.Fatal(err)
	}
	cmd, err := runtimeCommands{rt}.LoadCommand(ws, "", "review", "main")
	if err != nil || cmd.Prompt != "Review gently: main" {
		t.Fatalf("command %v %q", err, cmd.Prompt)
	}
	// A built-in skill, then turned off.
	if _, err := rt.CustomSet("skill.debugging", "---\ndescription: Mine\n---\nPrint everything."); err != nil {
		t.Fatal(err)
	}
	if s, _ := skills.Load(rt.dataDir, ws, "debugging"); s.Content != "---\ndescription: Mine\n---\nPrint everything." {
		t.Fatalf("skill %q", s.Content)
	}
	if err := rt.SkillEnabledSet("debugging", false); err != nil {
		t.Fatal(err)
	}
	if _, err := skills.Load(rt.dataDir, ws, "debugging"); err == nil {
		t.Fatal("a turned-off skill still loads")
	}
	if st, _ := rt.CustomGet("skill.debugging"); st.Enabled == nil || *st.Enabled {
		t.Fatalf("state %+v", st.Enabled)
	}
	if err := rt.SkillEnabledSet("debugging", true); err != nil || len(rt.loadSettings().DisabledSkills) != 0 {
		t.Fatalf("back on: %v %v", err, rt.loadSettings().DisabledSkills)
	}
	// Reset brings the defaults back.
	for _, id := range []string{"tool.shell.description", "prompt.tool-guidance", "subagent.explore", "command.review", "skill.debugging"} {
		if st, err := rt.CustomReset(id); err != nil || st.Customized {
			t.Fatalf("reset %s: %v %+v", id, err, st)
		}
	}
	if s, _ := skills.Load(rt.dataDir, ws, "debugging"); s.Source != skills.SourceBuiltin {
		t.Fatalf("skill after reset: %s", s.Source)
	}
}

func TestCustomizeChecksSettingsFiles(t *testing.T) {
	rt, _ := testRuntime(t)
	for _, c := range []struct{ id, text string }{
		{"file.settings", "not json"},
		{"file.settings", `{"maxSubagents":"many"}`},
		{"file.lsp", `{"servers":{"mylang":{"command":["x"]}}}`},
		{"file.lsp", `{"timeoutMs":1,"typo":true}`},
	} {
		if _, err := rt.CustomSet(c.id, c.text); err == nil {
			t.Errorf("%s accepted %s", c.id, c.text)
		}
	}
	if _, err := rt.CustomSet("file.settings", `{"maxSubagents": 2, "web": {"maxChars": 1000}}`); err != nil {
		t.Fatal(err)
	}
	if s := rt.loadSettings(); s.MaxSubagents != 2 || s.Web.MaxChars != 1000 {
		t.Fatalf("settings %+v", s)
	}
	// Placeholders a text dropped are reported, and the text still saves.
	st, err := rt.CustomSet("reminder.verify", "Run the tests before you stop.")
	if err != nil || len(st.Missing) != 1 || st.Missing[0] != "files" {
		t.Fatalf("missing %v %+v", err, st.Missing)
	}
}
