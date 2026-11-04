package tools

import (
	"encoding/json"
	"testing"
)

// TestResolveNameAliases covers the cross-harness aliases: a model
// that learned "bash" somewhere else must reach the shell tool.
func TestResolveNameAliases(t *testing.T) {
	reg := BuiltinRegistry()
	for _, tc := range []struct {
		in   string
		want string
	}{
		// The alias under test.
		{"bash", "shell"},
		{"Bash", "shell"},
		{"BASH", "shell"},
		{" sh ", "shell"},
		// Namespace and punctuation forms some models emit.
		{"functions.bash", "shell"},
		{"tools.bash", "shell"},
		{"functions.sh", "shell"},
		// Aliases that take shell's own arguments.
		{"execute_bash", "shell"},
		{"shell_command", "shell"},
		{"run_command", "shell"},
		{"execute_command", "shell"},
		{"execute-bash", "shell"},
		{"Execute Command", "shell"},
		// Canonical names keep working.
		{"shell", "shell"},
		{"read", "read"},
		{"Shell", "shell"},
	} {
		t.Run(tc.in, func(t *testing.T) {
			got, ok := reg.resolveName(tc.in)
			if !ok {
				t.Fatalf("resolveName(%q) did not resolve", tc.in)
			}
			if got != tc.want {
				t.Fatalf("resolveName(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestResolveNameAliasNeedsRegisteredTarget: an alias only resolves
// when the tool it stands for is in this registry. Otherwise a
// read-only subset (plan mode, a subagent) would hand the model shell
// access it was never granted.
func TestResolveNameAliasNeedsRegisteredTarget(t *testing.T) {
	reg := NewRegistry()
	reg.Register(ReadTool{})
	if got, ok := reg.resolveName("bash"); ok {
		t.Fatalf("bash resolved to %q in a registry without shell", got)
	}
	// The canonical name is likewise absent, so both fail the same way.
	if _, ok := reg.resolveName("shell"); ok {
		t.Fatal("shell resolved in a registry without shell")
	}
}

// TestResolveNameRegisteredToolWins: a real tool named "bash" is
// never shadowed by the alias table.
func TestResolveNameRegisteredToolWins(t *testing.T) {
	reg := NewRegistry()
	reg.Register(echoTool{name: "bash"})
	reg.Register(NewShellTool())
	if got, ok := reg.resolveName("bash"); !ok || got != "bash" {
		t.Fatalf("got %q, %v; want the registered bash tool", got, ok)
	}
}

// TestUnknownNameStaysUnresolved: an alias is not a wildcard. A name
// with no equivalent must still fail so the model is told to adapt.
func TestUnknownNameStaysUnresolved(t *testing.T) {
	reg := BuiltinRegistry()
	for _, name := range []string{"patch", "str_replace_editor", "todo_write", "", "   ", "functions."} {
		if got, ok := reg.resolveName(name); ok {
			t.Fatalf("resolveName(%q) unexpectedly resolved to %q", name, got)
		}
	}
}

// TestCanonicalName covers the helper the agent records calls with.
func TestCanonicalName(t *testing.T) {
	reg := BuiltinRegistry()
	if got := reg.CanonicalName("bash"); got != "shell" {
		t.Fatalf("CanonicalName(bash) = %q, want shell", got)
	}
	if got := reg.CanonicalName("read"); got != "read" {
		t.Fatalf("CanonicalName(read) = %q, want read", got)
	}
	// Unresolvable names are returned unchanged so the caller's error
	// still names what the model actually asked for.
	if got := reg.CanonicalName("patch"); got != "patch" {
		t.Fatalf("CanonicalName(patch) = %q, want patch unchanged", got)
	}
	var nilReg *Registry
	if got := nilReg.CanonicalName("bash"); got != "bash" {
		t.Fatalf("nil registry = %q, want bash unchanged", got)
	}
}

// TestCallThroughBashAlias runs the real thing: a call emitted as
// "bash" executes the shell tool and comes back reporting the name
// that ran.
func TestCallThroughBashAlias(t *testing.T) {
	ctx, _ := testCtx(t)
	reg := BuiltinRegistry()
	res := reg.Call(&ToolCall{ID: "c1", Name: "bash", Arguments: `{"command":"echo aliased"}`}, ctx)
	if res.IsError {
		t.Fatalf("call failed: %s", res.Content)
	}
	if res.Name != "shell" {
		t.Fatalf("result name = %q, want shell", res.Name)
	}
	var resp ShellResponse
	if err := json.Unmarshal([]byte(res.Content), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Output == nil || *resp.Output != "aliased\n" {
		t.Fatalf("got %+v, want output %q", resp, "aliased\n")
	}
}

// TestPrepareThroughBashAlias: the gate and hooks judge what will run,
// so Prepare must resolve an alias exactly as Call does.
func TestPrepareThroughBashAlias(t *testing.T) {
	reg := BuiltinRegistry()
	name, args, ok := reg.Prepare(&ToolCall{ID: "c1", Name: "Bash", Arguments: `{"command":"echo hi"}`})
	if !ok {
		t.Fatal("Prepare did not resolve the alias")
	}
	if name != "shell" {
		t.Fatalf("Prepare name = %q, want shell", name)
	}
	if string(args) != `{"command":"echo hi"}` {
		t.Fatalf("Prepare args = %s", args)
	}
}

// TestAliasedCallKeepsSafetyClass: an aliased call must carry the
// target's properties, or the gate and batching would treat it as an
// unknown, harmless tool.
func TestAliasedCallKeepsSafetyClass(t *testing.T) {
	reg := BuiltinRegistry()
	for _, name := range []string{"bash", "sh", "execute_command"} {
		if !reg.Mutates(name) {
			t.Fatalf("%q should be reported as mutating, like shell", name)
		}
		if !reg.ParallelSafe(name) {
			t.Fatalf("%q should be reported parallel-safe, like shell", name)
		}
		if reg.IsExtension(name) {
			t.Fatalf("%q must not look like an extension tool", name)
		}
		if _, isMCP := reg.MCPInfo(name); isMCP {
			t.Fatalf("%q must not look like an MCP tool", name)
		}
	}
}

// TestAliasTableTargetsAreRealTools guards against a typo in the table
// pointing at a tool that does not exist.
func TestAliasTableTargetsAreRealTools(t *testing.T) {
	known := map[string]bool{}
	for _, n := range BuiltinCatalog().Names() {
		known[n] = true
	}
	for alias, target := range toolAliases {
		if !known[target] {
			t.Fatalf("alias %q points at %q, which is not a built-in tool", alias, target)
		}
		if alias == target {
			t.Fatalf("alias %q points at itself", alias)
		}
	}
}
