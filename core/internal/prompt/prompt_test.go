package prompt

import (
	"strings"
	"testing"
)

func TestKeepsStablePrefixOrder(t *testing.T) {
	p := AssembleSystemPrompt("base", "guide", "env", "examples", "schemas")
	bp, gp, ep := strings.Index(p, "base"), strings.Index(p, "guide"), strings.Index(p, "env")
	if !(bp < gp && gp < ep) {
		t.Fatal("prefix order unstable")
	}
}

func TestToolSystemMentionsWorkspace(t *testing.T) {
	p := ToolSystem(SystemPrompt, "/tmp/proj")
	if !strings.Contains(p, "/tmp/proj") || !strings.Contains(p, "full absolute paths") {
		t.Fatal("workspace missing from prompt")
	}
}

// ---- merged from prompt_extra_test.go ----

func TestAssembleIncludesSchemasOnlyWhenSet(t *testing.T) {
	with := AssembleSystemPrompt("base", "guide", "env", "examples", "schemas")
	if !strings.Contains(with, "schemas") {
		t.Fatalf("got %q", with)
	}
	without := AssembleSystemPrompt("base", "guide", "env", "examples", "")
	if strings.Contains(without, "schemas") {
		t.Fatalf("got %q", without)
	}
	// Environment block is always present and ordered after guidance.
	bp, gp, ep := strings.Index(with, "base"), strings.Index(with, "guide"), strings.Index(with, "Environment:")
	if !(bp < gp && gp < ep) {
		t.Fatalf("order wrong: %q", with)
	}
}

func TestEnvironmentBlockAndOSName(t *testing.T) {
	block := EnvironmentBlock("Linux", "/tmp/w")
	if !strings.Contains(block, "Linux") || !strings.Contains(block, "/tmp/w") || !strings.Contains(block, "inside or outside") {
		t.Fatalf("got %q", block)
	}
	if OSName() == "" {
		t.Fatal("empty OS name")
	}
	if !strings.Contains(ToolSystem("b", "/w"), "b") {
		t.Fatal("base missing")
	}
}

func TestSystemPromptContent(t *testing.T) {
	if !strings.Contains(SystemPrompt, "workspace") {
		t.Fatalf("system prompt lost workspace scope")
	}
	if g := ToolGuidance(); !strings.Contains(g, "shell") || !strings.Contains(g, "full absolute paths") {
		t.Fatalf("guidance changed")
	}
	if !strings.Contains(UsageExamples(), "overwrite") || !strings.Contains(UsageExamples(), "replace_all") {
		t.Fatalf("examples changed")
	}
}

// TestGuidanceIsOSSpecific pins the per-OS compilation: a Windows turn
// never sees sh/Unix-tool text, and a Unix turn never sees
// PowerShell/cmd.exe text.
func TestGuidanceIsOSSpecific(t *testing.T) {
	win := ToolGuidanceFor("windows")
	if !strings.Contains(win, "PowerShell") {
		t.Fatalf("windows guidance lost PowerShell: %q", win[:120])
	}
	if strings.Contains(win, "sh:") || strings.Contains(win, "Linux-style commands") {
		t.Fatalf("windows guidance leaks unix shell text")
	}
	for _, goos := range []string{"linux", "darwin"} {
		g := ToolGuidanceFor(goos)
		if strings.Contains(g, "PowerShell") || strings.Contains(g, "cmd.exe") {
			t.Fatalf("%s guidance leaks windows shell text", goos)
		}
		if !strings.Contains(g, "sh") {
			t.Fatalf("%s guidance lost sh", goos)
		}
	}
	winEx := UsageExamplesFor("windows")
	if !strings.Contains(winEx, "Get-ChildItem") {
		t.Fatal("windows examples lost PowerShell")
	}
	if strings.Contains(winEx, "find ~/") || strings.Contains(winEx, `"ls `) {
		t.Fatal("windows examples leak unix commands")
	}
	nixEx := UsageExamplesFor("linux")
	if !strings.Contains(nixEx, "find ~/") {
		t.Fatal("unix examples lost find")
	}
	if strings.Contains(nixEx, "PowerShell") || strings.Contains(nixEx, "Get-ChildItem") || strings.Contains(nixEx, "cmd.exe") {
		t.Fatal("unix examples leak windows commands")
	}
}

// TestMachineDetailsAlwaysInjected pins the guarantee the UI states:
// every compiled turn carries the machine (OS + arch) and the session
// workspace, no matter what the prompt file holds.
func TestMachineDetailsAlwaysInjected(t *testing.T) {
	p := AssembleSystemPrompt("user base", ToolGuidanceFor("linux"), EnvironmentBlock(OSName(), "/w/proj"), UsageExamplesFor("linux"), "")
	for _, want := range []string{"user base", "/w/proj", "scope flag", "arch "} {
		if !strings.Contains(p, want) {
			t.Fatalf("compiled prompt lost %q", want)
		}
	}
	if !strings.Contains(p, "Linux") && !strings.Contains(p, "Windows") && !strings.Contains(p, "macOS") {
		t.Fatal("compiled prompt lost the OS name")
	}
}
