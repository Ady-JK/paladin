package hooks

import (
	"testing"
)

func TestParseReadsAllEventsAndHandlers(t *testing.T) {
	cfg, warn, err := Parse([]byte(`{
	  "hooks": {
	    "PreToolUse": [{"matcher": "shell|Bash", "hooks": [{"type": "command", "command": "a.sh", "timeout": 5, "statusMessage": "checking"}]}],
	    "Stop": [{"hooks": [{"type": "command", "command": "stop.sh"}]}],
	    "Notification": [{"hooks": [{"type": "command", "command": "x"}]}]
	  },
	  "model": "ignored by us (claude settings.json)"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	pre := cfg[PreToolUse]
	if len(pre) != 1 || pre[0].Matcher != "shell|Bash" || len(pre[0].Hooks) != 1 {
		t.Fatalf("pre = %+v", pre)
	}
	h := pre[0].Hooks[0]
	if h.Command != "a.sh" || h.Timeout != 5 || h.StatusMessage != "checking" {
		t.Fatalf("handler = %+v", h)
	}
	if len(cfg[Stop]) != 1 {
		t.Fatalf("stop = %+v", cfg[Stop])
	}
	if len(warn) != 1 {
		t.Fatalf("want one warning for unsupported event, got %v", warn)
	}
}

func TestParseSkipsNonCommandHandlers(t *testing.T) {
	cfg, warn, err := Parse([]byte(`{"hooks": {"Stop": [{"hooks": [{"type": "prompt", "prompt": "x"}, {"command": "ok.sh"}]}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg[Stop][0].Hooks; len(got) != 1 || got[0].Command != "ok.sh" {
		t.Fatalf("hooks = %+v", got)
	}
	if len(warn) != 1 {
		t.Fatalf("warn = %v", warn)
	}
}

func TestParseRejectsBadJSONAndBadMatcher(t *testing.T) {
	if _, _, err := Parse([]byte(`{"hooks": `)); err == nil {
		t.Fatal("want JSON error")
	}
	if _, _, err := Parse([]byte(`{"hooks": {"PreToolUse": [{"matcher": "(", "hooks": [{"command": "x"}]}]}}`)); err == nil {
		t.Fatal("want matcher error")
	}
}

func TestParseWithoutHooksKeyIsEmpty(t *testing.T) {
	cfg, _, err := Parse([]byte(`{"permissions": {}}`))
	if err != nil || len(cfg) != 0 {
		t.Fatalf("cfg=%v err=%v", cfg, err)
	}
}

func TestMatcherForms(t *testing.T) {
	cases := []struct {
		pattern string
		input   string
		want    bool
	}{
		{"", "anything", true},
		{"*", "anything", true},
		{"shell", "shell", true},
		{"shell", "shellx", false},
		{"read|write", "write", true},
		{"mcp__.*", "mcp__fs__read", true},
		{"mcp__.*", "shell", false},
	}
	for _, c := range cases {
		m, err := newMatcher(c.pattern)
		if err != nil {
			t.Fatal(err)
		}
		if got := m.matches([]string{c.input}); got != c.want {
			t.Errorf("%q vs %q = %v", c.pattern, c.input, got)
		}
	}
}

func TestToolNamesIncludeClaudeAliases(t *testing.T) {
	m, _ := newMatcher("Bash")
	if !m.matches(toolNames("shell")) {
		t.Fatal("Bash should select shell")
	}
	m, _ = newMatcher("Write|Edit")
	if !m.matches(toolNames("edit")) || !m.matches(toolNames("write")) {
		t.Fatal("Write|Edit should select edit and write")
	}
	if m.matches(toolNames("read")) {
		t.Fatal("Write|Edit must not select read")
	}
}
