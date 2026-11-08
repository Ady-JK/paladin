package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Ady-JK/paladin/core/internal/agent"
	"github.com/Ady-JK/paladin/core/internal/config"
	"github.com/Ady-JK/paladin/core/internal/mcp"
	"github.com/Ady-JK/paladin/core/internal/tools"
	"github.com/Ady-JK/paladin/core/internal/toolset"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

type agentTestTool struct {
	name string
	info *tools.MCPInfo
}

func TestReadWriteChildInheritsParentPlanMode(t *testing.T) {
	model, calls := routedModel(t, func(c modelCall) modelReply {
		if c.Child {
			return modelReply{Text: "child finished"}
		}
		if _, ok := c.Results["wait_agent"]; ok {
			return modelReply{Text: "done"}
		}
		if _, ok := c.Results["spawn_agent"]; ok {
			return modelReply{Calls: []mockToolCall{call("wait_agent", map[string]any{"timeout_ms": 10000})}}
		}
		return modelReply{Calls: []mockToolCall{call("spawn_agent", map[string]any{"agent_type": "writer", "task": "inspect"})}}
	})
	rt, data := testRuntime(t)
	t.Cleanup(func() { rt.ShutdownTurns(5 * time.Second) })
	if _, err := rt.AddProvider(config.ProviderInput{Name: "LocalTest", ApiURL: model.URL, ApiType: "openai", ApiKey: "local-test-key"}); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.SetDefaultProvider("LocalTest"); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(data, "agents", "writer")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	definition := `{"name":"writer","description":"Writes","permissions":"read-write","tools":{"read":true,"write":true,"edit":true},"mcp":{},"role":"Do the task."}`
	if err := os.WriteFile(filepath.Join(dir, "agent.json"), []byte(definition), 0600); err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	parent, err := rt.OpenSession("", ws)
	if err != nil {
		t.Fatal(err)
	}
	_, done, err := rt.SubmitTurn(agent.Request{SessionID: parent.SessionID, Cwd: ws, Message: "delegate", Mode: agent.ModePlan, Permissions: "full-access"}, SubmitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		if result.Error != "" {
			t.Fatal(result.Error)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("turn timed out")
	}
	children := childIDs(t, rt, ws, parent.SessionID)
	if len(children) != 1 {
		t.Fatalf("got %d children", len(children))
	}
	saved := loadChild(t, rt, ws, children[0]).Meta.Subagent.Definition
	if saved == nil || !saved.ReadWrite || saved.Role != "Do the task." {
		t.Fatalf("child definition not persisted: %+v", saved)
	}
	for _, c := range calls() {
		if !c.Child {
			continue
		}
		for _, name := range c.Tools {
			if name == "write" || name == "edit" {
				t.Fatalf("plan child received mutating tool %s", name)
			}
		}
		return
	}
	t.Fatal("child model was not called")
}

func TestCreateAgentCatalogIncludesReadyMCPTools(t *testing.T) {
	rt, ws := testRuntime(t)
	if _, err := rt.MCPSetConfig(mcp.ScopeGlobal, "", dummyConfig(t)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.mcp.Close() })
	text := rt.agentCapabilityContext(ws, "")
	if !strings.Contains(text, "MCP dummy / echo") {
		t.Fatalf("missing MCP tool: %s", text)
	}
}

func (t agentTestTool) Name() string                                         { return t.name }
func (t agentTestTool) Definition() tools.ToolDefinition                     { return tools.ToolDefinition{Name: t.name} }
func (t agentTestTool) Call(json.RawMessage, *tools.ToolCtx) (string, error) { return "ok", nil }
func (t agentTestTool) MCPInfo() tools.MCPInfo                               { return *t.info }

type agentTestExtension struct{ name string }

func (t agentTestExtension) Name() string { return t.name }
func (t agentTestExtension) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{Name: t.name}
}
func (t agentTestExtension) Call(json.RawMessage, *tools.ToolCtx) (string, error) { return "ok", nil }
func (agentTestExtension) Extension() bool                                        { return true }

func TestFileSubagentsAreDiscoveredAndReloaded(t *testing.T) {
	rt, data := testRuntime(t)
	root := filepath.Join(data, "agents")
	dir := filepath.Join(root, "writer")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "agent.json")
	write := func(role string) {
		t.Helper()
		text := `{"name":"writer","description":"Fix code","permissions":"read-write","tools":{"read":true,"edit":true},"mcp":{},"role":"` + role + `"}`
		if err := os.WriteFile(file, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("First role")
	kind, ok := rt.findConfiguredSubagent("writer")
	if !ok || !kind.ReadWrite || !kind.allows("edit") || !strings.Contains(kind.prompt(), "First role") {
		t.Fatalf("bad kind: %+v", kind)
	}
	saved := &transcript.Subagent{Type: kind.Name, Definition: &transcript.SubagentDefinition{Role: kind.Role, Tools: kind.Tools, ReadWrite: kind.ReadWrite, FileBased: true}}
	tool, _ := rt.tools.Get("spawn_agent")
	if !strings.Contains(tool.Definition().Description, "writer") {
		t.Fatal("custom agent missing from spawn tool")
	}
	write("Updated role")
	kind, ok = rt.findConfiguredSubagent("writer")
	if !ok || !strings.Contains(kind.prompt(), "Updated role") {
		t.Fatalf("not reloaded: %+v", kind)
	}
	old, ok := rt.subagentDefinition(saved)
	if !ok || !strings.Contains(old.prompt(), "First role") {
		t.Fatalf("existing child changed policy: %+v", old)
	}
	if _, err := os.Stat(filepath.Join(root, "explore", "agent.json")); err != nil {
		t.Fatalf("built-in template missing: %v", err)
	}
}

func TestSpawnDefinitionWithoutAgentsHasValidTypeSchema(t *testing.T) {
	definition := spawnDefFor(nil)
	parameters, ok := definition.Parameters.(map[string]any)
	if !ok {
		t.Fatal("missing parameters")
	}
	properties, ok := parameters["properties"].(map[string]any)
	if !ok {
		t.Fatal("missing properties")
	}
	agentType, ok := properties["agent_type"].(map[string]any)
	if !ok || agentType["type"] != "string" {
		t.Fatalf("invalid agent_type schema: %+v", agentType)
	}
	if _, present := agentType["enum"]; present {
		t.Fatalf("empty agent catalog exposed an invalid enum: %+v", agentType)
	}
}

func TestInternalMemoryReviewerKeepsItsRestrictedTools(t *testing.T) {
	rt, data := testRuntime(t)
	file := filepath.Join(data, "agents", "memory-review", "agent.json")
	definition := `{"name":"memory-review","description":"Review memory","permissions":"read-write","tools":{"write":true,"shell":true},"mcp":{},"role":"Review durable facts."}`
	if err := os.WriteFile(file, []byte(definition), 0600); err != nil {
		t.Fatal(err)
	}
	kind, ok := rt.findConfiguredSubagent("memory-review")
	if !ok || kind.ReadWrite || kind.allows("write") || kind.allows("shell") || !kind.allows("memory") || !strings.Contains(kind.Role, "durable facts") {
		t.Fatalf("memory review escaped its limits or lost its role: %+v", kind)
	}
}

func TestConfiguredSubagentToolSelectionAndParentSwitches(t *testing.T) {
	rt, ws := testRuntime(t)
	parent, err := rt.transcripts.Create(ws, "local", "test")
	if err != nil {
		t.Fatal(err)
	}
	child := transcript.Session{Meta: transcript.Meta{Cwd: ws, Subagent: &transcript.Subagent{ParentID: parent.Meta.SessionID}}}
	read := agentTestTool{name: "mcp__local__lookup", info: &tools.MCPInfo{Server: "local", Tool: "lookup", ReadOnly: true}}
	write := agentTestTool{name: "mcp__local__change", info: &tools.MCPInfo{Server: "local", Tool: "change"}}
	ext := agentTestExtension{name: "extension__sample__run"}
	all := rt.tools.With(read, write, ext)
	kind := subagentKind{Name: "custom", ReadWrite: true, Tools: []string{"read", "edit", "browser", "memory", ext.name, "spawn_agent"}, MCP: map[string][]string{"local": {"lookup", "change"}}}
	check := func(reg *tools.Registry, name string, want bool) {
		t.Helper()
		_, got := reg.Get(name)
		if got != want {
			t.Fatalf("%s present=%v want=%v", name, got, want)
		}
	}
	reg := rt.childToolsFrom(child, kind, all)
	for _, name := range []string{"read", "edit", ext.name, read.name, write.name} {
		check(reg, name, true)
	}
	check(reg, "spawn_agent", false)
	kind.ReadWrite = false
	reg = rt.childToolsFrom(child, kind, all)
	for _, name := range []string{"edit", "browser", "memory", ext.name, write.name} {
		check(reg, name, false)
	}
	check(reg, read.name, true)
	kind.ReadWrite = true
	off := false
	if _, err := rt.ToolsSet(ToolScopeSession, ws, parent.Meta.SessionID, []string{"edit", read.name}, &off); err != nil {
		t.Fatal(err)
	}
	reg = rt.childToolsFrom(child, kind, all)
	check(reg, "edit", false)
	check(reg, read.name, false)
	check(reg, write.name, true)
	// A newly discovered tool remains unavailable until the parent accepts it.
	if err := rt.transcripts.UpdateMeta(ws, parent.Meta.SessionID, func(meta *transcript.Meta) error {
		meta.Tools = &toolset.State{Seeded: true, Known: []string{"read"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	reg = rt.childToolsFrom(child, kind, all)
	check(reg, "read", true)
	check(reg, ext.name, false)
	check(reg, write.name, false)
	child.Meta.Subagent.ParentID = "missing-parent"
	if got := rt.childToolsFrom(child, kind, all).Len(); got != 0 {
		t.Fatalf("missing parent exposed %d tools", got)
	}
}

func TestCreateAgentSkillAndCommandReceiveCompactCapabilities(t *testing.T) {
	rt, ws := testRuntime(t)
	pluginDir := filepath.Join(rt.dataDir, "config", "extensions", "example")
	if err := os.MkdirAll(pluginDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rt.dataDir, "config", "extensions.json"), []byte(`{"enabled":["example"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	manifest := `{"entry":"index.js","tools":[{"name":"hello","description":"Says hello. Long details must be omitted.","parameters":{"type":"object"}}]}`
	if err := os.WriteFile(filepath.Join(pluginDir, "paladin-plugin.json"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "index.js"), []byte(`export default {tools:[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	parent, err := rt.transcripts.Create(ws, "local", "test")
	if err != nil {
		t.Fatal(err)
	}
	skill, err := (runtimeSkills{rt}).LoadSkillForSession(ws, parent.Meta.SessionID, "create-agent")
	if err != nil {
		t.Fatal(err)
	}
	command, err := (runtimeCommands{rt}).LoadCommand(ws, parent.Meta.SessionID, "create-agent", "make a reviewer")
	if err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{skill.Content, command.Prompt} {
		for _, want := range []string{"Agent definitions directory:", "builtin read", "extension__example__hello", "permissions", "make"} {
			if want == "make" && content == skill.Content {
				continue
			}
			if !strings.Contains(strings.ToLower(content), strings.ToLower(want)) {
				t.Fatalf("missing %q from creation context", want)
			}
		}
		if strings.Contains(content, "Long details must be omitted") {
			t.Fatal("full extension description leaked into catalog")
		}
	}
	tool, ok := rt.tools.Get("skill")
	if !ok {
		t.Fatal("skill tool missing")
	}
	result, err := tool.Call(json.RawMessage(`{"name":"create-agent"}`), &tools.ToolCtx{Workspace: ws, SessionID: parent.Meta.SessionID})
	if err != nil || !strings.Contains(result, "builtin read") {
		t.Fatalf("skill tool did not include capabilities: %s %v", result, err)
	}
	if got := shortCapabilityDescription(strings.Repeat("long ", 100)); len([]rune(got)) > 160 {
		t.Fatalf("description too long: %d", len([]rune(got)))
	}
}
