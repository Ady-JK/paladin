// Package permission decides whether a tool call may run, asks the user
// when it may not, and remembers their answers as rules. It mirrors
// Codex's approval presets and OpenCode's allow/deny rules: a preset sets
// the baseline, rules (global, per folder, per session) refine it, and
// destructive shell commands always ask unless the preset is full access.
package permission

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Ady-JK/paladin/core/internal/sandbox"
)

// Preset is a session's baseline permission level.
type Preset string

const (
	ReadOnly   Preset = "read-only"
	Auto       Preset = "auto"
	FullAccess Preset = "full-access"
)

// ParsePreset maps the wire value; empty means Auto.
func ParsePreset(s string) (Preset, error) {
	switch Preset(strings.TrimSpace(s)) {
	case "", Auto:
		return Auto, nil
	case ReadOnly:
		return ReadOnly, nil
	case FullAccess:
		return FullAccess, nil
	}
	return "", fmt.Errorf("permissions must be %q, %q or %q, got %q", ReadOnly, Auto, FullAccess, s)
}

// Rule tools and actions.
const (
	ToolShell    = "shell"
	ToolWrite    = "write"
	ActionAllow  = "allow"
	ActionDeny   = "deny"
	matchAnyRule = "*"
)

// Rule allows or denies shell commands by word prefix ("npm test") or
// writes by directory ("/etc"). "*" matches everything of that tool.
type Rule struct {
	Tool    string `json:"tool"`
	Pattern string `json:"pattern"`
	Action  string `json:"action"`
}

func (r Rule) validate() error {
	switch r.Tool {
	case ToolShell, ToolWrite:
	default:
		return fmt.Errorf("rule tool must be %q or %q, got %q", ToolShell, ToolWrite, r.Tool)
	}
	if r.Action != ActionAllow && r.Action != ActionDeny {
		return fmt.Errorf("rule action must be %q or %q, got %q", ActionAllow, ActionDeny, r.Action)
	}
	p := strings.TrimSpace(r.Pattern)
	if p == "" {
		return fmt.Errorf("rule pattern is required")
	}
	if r.Tool == ToolWrite && p != matchAnyRule && !filepath.IsAbs(p) {
		return fmt.Errorf("write rule pattern must be an absolute directory, got %q", p)
	}
	return nil
}

func (r Rule) normalized() Rule {
	r.Pattern = strings.TrimSpace(r.Pattern)
	if r.Tool == ToolShell {
		r.Pattern = strings.Join(strings.Fields(r.Pattern), " ")
	} else if r.Pattern != matchAnyRule {
		r.Pattern = filepath.Clean(r.Pattern)
	}
	return r
}

// Verdict is the outcome of Decide.
type Verdict int

const (
	Allow Verdict = iota
	Ask
	Deny
)

// Replies a user can give on an approval card.
const (
	ReplyOnce    = "once"
	ReplySession = "session"
	ReplyFolder  = "folder"
	ReplyDeny    = "deny"
)

// Decision is what to do with one call.
type Decision struct {
	Verdict     Verdict
	Reasons     []string
	Destructive bool
	// Sandbox is the confinement for a shell call that runs (after
	// approval, when asked).
	Sandbox sandbox.Mode
	// Remember holds the rules "this session"/"this folder" would save;
	// empty when the call cannot be generalized safely.
	Remember []Rule
	Choices  []string
	// Rule is the rule that decided the call, if any.
	Rule *Rule
}

// Decide applies the preset and rules to one request. Deny rules win
// over everything, including full access.
func Decide(p Preset, req Request, rules []Rule) Decision {
	if r := denyingRule(req, rules); r != nil {
		return Decision{Verdict: Deny, Rule: r, Reasons: []string{"blocked by a rule"}}
	}
	if p == FullAccess {
		return Decision{Verdict: Allow, Sandbox: sandbox.ModeNone}
	}
	switch req.Kind {
	case KindWrite:
		return decideWrite(p, req, rules)
	case KindShell:
		return decideShell(p, req, rules)
	}
	return Decision{Verdict: Allow}
}

func decideWrite(p Preset, req Request, rules []Rule) Decision {
	if r := allowingPath(req.Path, rules); r != nil {
		return Decision{Verdict: Allow, Rule: r}
	}
	var reasons []string
	switch {
	case req.Outside:
		reasons = append(reasons, "outside the workspace")
	case p == ReadOnly:
		reasons = append(reasons, "read-only mode")
	default:
		return Decision{Verdict: Allow}
	}
	remember := []Rule{{Tool: ToolWrite, Pattern: filepath.Dir(req.Path), Action: ActionAllow}}
	return Decision{Verdict: Ask, Reasons: reasons, Remember: remember, Choices: allChoices()}
}

func decideShell(p Preset, req Request, rules []Rule) Decision {
	safeMode := sandbox.ModeWorkspaceWrite
	if p == ReadOnly {
		safeMode = sandbox.ModeReadOnly
	}
	if req.Destructive {
		return Decision{Verdict: Ask, Destructive: true, Reasons: []string{"destructive command"},
			Sandbox: sandbox.ModeWorkspaceWrite, Choices: []string{ReplyOnce, ReplyDeny}}
	}
	if req.Safe {
		return Decision{Verdict: Allow, Sandbox: safeMode}
	}
	if req.Unparsed {
		return Decision{Verdict: Ask, Reasons: []string{"uses shell substitution"},
			Sandbox: sandbox.ModeWorkspaceWrite, Choices: []string{ReplyOnce, ReplyDeny}}
	}
	var missing []Rule
	seen := map[string]bool{}
	var matched *Rule
	for _, seg := range req.Segments {
		if seg.safe {
			continue
		}
		if r := allowingCommand(seg.words, rules); r != nil {
			matched = r
			continue
		}
		prefix := prefixOf(seg.words)
		if prefix != "" && !seen[prefix] {
			seen[prefix] = true
			missing = append(missing, Rule{Tool: ToolShell, Pattern: prefix, Action: ActionAllow})
		}
	}
	if len(missing) == 0 {
		return Decision{Verdict: Allow, Sandbox: sandbox.ModeWorkspaceWrite, Rule: matched}
	}
	return Decision{Verdict: Ask, Reasons: []string{"not on the safe list"}, Sandbox: sandbox.ModeWorkspaceWrite,
		Remember: missing, Choices: allChoices()}
}

func allChoices() []string { return []string{ReplyOnce, ReplySession, ReplyFolder, ReplyDeny} }

func denyingRule(req Request, rules []Rule) *Rule {
	for i := range rules {
		r := &rules[i]
		if r.Action != ActionDeny {
			continue
		}
		switch req.Kind {
		case KindWrite:
			if r.Tool == ToolWrite && pathMatches(r.Pattern, req.Path) {
				return r
			}
		case KindShell:
			if r.Tool != ToolShell {
				continue
			}
			if r.Pattern == matchAnyRule {
				return r
			}
			for _, seg := range req.Segments {
				if commandMatches(r.Pattern, seg.words) {
					return r
				}
			}
		}
	}
	return nil
}

func allowingPath(path string, rules []Rule) *Rule {
	for i := range rules {
		r := &rules[i]
		if r.Tool == ToolWrite && r.Action == ActionAllow && pathMatches(r.Pattern, path) {
			return r
		}
	}
	return nil
}

func allowingCommand(words []string, rules []Rule) *Rule {
	for i := range rules {
		r := &rules[i]
		if r.Tool == ToolShell && r.Action == ActionAllow && commandMatches(r.Pattern, words) {
			return r
		}
	}
	return nil
}

func pathMatches(pattern, path string) bool {
	if pattern == matchAnyRule {
		return true
	}
	pattern = filepath.Clean(pattern)
	return path == pattern || strings.HasPrefix(path, strings.TrimSuffix(pattern, string(filepath.Separator))+string(filepath.Separator))
}

func commandMatches(pattern string, words []string) bool {
	if pattern == matchAnyRule {
		return true
	}
	want := strings.Fields(pattern)
	words = normalizeWords(words)
	if len(want) == 0 || len(words) < len(want) {
		return false
	}
	for i, w := range want {
		if words[i] != w {
			return false
		}
	}
	return true
}
