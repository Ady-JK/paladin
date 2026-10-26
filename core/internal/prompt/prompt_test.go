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
