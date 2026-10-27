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
	p := ToolSystem(SystemPrompt, ToolGuidance, "/tmp/proj")
	if !strings.Contains(p, "/tmp/proj") || !strings.Contains(p, "workspace/<path>") {
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
	if !strings.Contains(block, "Linux") || !strings.Contains(block, "/tmp/w") || !strings.Contains(block, "workspace/<path>") {
		t.Fatalf("got %q", block)
	}
	if OSName() == "" {
		t.Fatal("empty OS name")
	}
	if !strings.Contains(ToolSystem("b", "g", "/w"), "b") {
		t.Fatal("base missing")
	}
}

func TestSystemPromptContent(t *testing.T) {
	if !strings.Contains(SystemPrompt, "workspace") {
		t.Fatalf("system prompt lost workspace scope")
	}
	if !strings.Contains(ToolGuidance, "shell") || !strings.Contains(ToolGuidance, "workspace/<path>") {
		t.Fatalf("guidance changed")
	}
	if !strings.Contains(UsageExamples, "overwrite") || !strings.Contains(UsageExamples, "replace_all") {
		t.Fatalf("examples changed")
	}
}
