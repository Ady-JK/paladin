// Package hooks runs user scripts at fixed points of a turn: before and
// after tool calls, when a prompt is submitted, when a session starts,
// around compaction and when the agent wants to stop. The config shape
// and the stdin/stdout contract follow Claude Code and Codex, so their
// hooks.json / settings.json files work unchanged.
package hooks

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

type Event string

const (
	PreToolUse       Event = "PreToolUse"
	PostToolUse      Event = "PostToolUse"
	UserPromptSubmit Event = "UserPromptSubmit"
	SessionStart     Event = "SessionStart"
	Stop             Event = "Stop"
	PreCompact       Event = "PreCompact"
	PostCompact      Event = "PostCompact"
)

// Events lists the supported events in display order.
var Events = []Event{SessionStart, UserPromptSubmit, PreToolUse, PostToolUse, Stop, PreCompact, PostCompact}

func supported(ev Event) bool {
	for _, e := range Events {
		if e == ev {
			return true
		}
	}
	return false
}

// Handler is one command hook.
type Handler struct {
	Type           string `json:"type,omitempty"`
	Command        string `json:"command"`
	CommandWindows string `json:"commandWindows,omitempty"`
	Timeout        int    `json:"timeout,omitempty"` // seconds
	StatusMessage  string `json:"statusMessage,omitempty"`
}

// Group is a matcher plus the handlers it selects.
type Group struct {
	Matcher string    `json:"matcher,omitempty"`
	Hooks   []Handler `json:"hooks"`
}

// Config maps each event to its groups.
type Config map[Event][]Group

// Parse reads the "hooks" key of a Paladin/Codex hooks.json or a Claude
// settings.json. Unknown events and non-command handlers are skipped
// with a warning; bad JSON or a bad matcher regex is an error.
func Parse(data []byte) (Config, []string, error) {
	var doc struct {
		Hooks map[string][]Group `json:"hooks"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, nil, fmt.Errorf("invalid JSON: %w", err)
	}
	cfg := Config{}
	var warn []string
	for name, groups := range doc.Hooks {
		ev := Event(name)
		if !supported(ev) {
			warn = append(warn, fmt.Sprintf("event %q is not supported", name))
			continue
		}
		for _, g := range groups {
			if _, err := newMatcher(g.Matcher); err != nil {
				return nil, nil, fmt.Errorf("%s matcher %q: %w", name, g.Matcher, err)
			}
			kept := Group{Matcher: g.Matcher}
			for _, h := range g.Hooks {
				if h.Type != "" && h.Type != "command" {
					warn = append(warn, fmt.Sprintf("%s: %q hooks are not supported", name, h.Type))
					continue
				}
				if strings.TrimSpace(h.Command) == "" && strings.TrimSpace(h.CommandWindows) == "" {
					warn = append(warn, fmt.Sprintf("%s: hook without a command", name))
					continue
				}
				kept.Hooks = append(kept.Hooks, h)
			}
			if len(kept.Hooks) > 0 {
				cfg[ev] = append(cfg[ev], kept)
			}
		}
	}
	return cfg, warn, nil
}

// matcher selects handlers: "" or "*" match everything, a plain
// name list ("read|write") matches exactly, anything else is a regex.
type matcher struct {
	all   bool
	exact []string
	re    *regexp.Regexp
}

var matchAll = matcher{all: true}

var plainNames = regexp.MustCompile(`^[A-Za-z0-9_|]+$`)

func newMatcher(pattern string) (matcher, error) {
	switch {
	case pattern == "" || pattern == "*":
		return matchAll, nil
	case plainNames.MatchString(pattern):
		return matcher{exact: strings.Split(pattern, "|")}, nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return matcher{}, err
	}
	return matcher{re: re}, nil
}

func (m matcher) matches(names []string) bool {
	if m.all {
		return true
	}
	for _, n := range names {
		if m.re != nil {
			if m.re.MatchString(n) {
				return true
			}
			continue
		}
		for _, e := range m.exact {
			if e == n {
				return true
			}
		}
	}
	return false
}

// claudeAliases lets Claude-style matchers ("Bash", "Write|Edit")
// select the equivalent Paladin tools.
var claudeAliases = map[string][]string{
	"shell": {"Bash"},
	"write": {"Write"},
	"edit":  {"Edit", "MultiEdit"},
	"read":  {"Read"},
	"todo":  {"TodoWrite"},
	"skill": {"Skill"},
}

// toolNames is what a tool matcher is tested against: the Paladin name
// plus its Claude aliases.
func toolNames(tool string) []string {
	return append([]string{tool}, claudeAliases[tool]...)
}
