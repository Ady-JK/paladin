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
	"github.com/Ady-JK/paladin/core/internal/tools"
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
	ToolMCP      = "mcp"
	ToolRead     = "read"
	ActionAllow  = "allow"
	ActionDeny   = "deny"
	matchAnyRule = "*"
)

// Rule allows or denies shell commands by word prefix ("npm test"),
// writes or outside-workspace reads by directory ("/etc") or MCP tools
// by "server" / "server/*" / "server/tool". "*" matches everything of
// that tool. An allow-write rule also allows reading there.
type Rule struct {
	Tool    string `json:"tool"`
	Pattern string `json:"pattern"`
	Action  string `json:"action"`
}

func (r Rule) validate() error {
	switch r.Tool {
	case ToolShell, ToolWrite, ToolMCP, ToolRead:
	default:
		return fmt.Errorf("rule tool must be %q, %q, %q or %q, got %q", ToolShell, ToolWrite, ToolRead, ToolMCP, r.Tool)
	}
	if r.Action != ActionAllow && r.Action != ActionDeny {
		return fmt.Errorf("rule action must be %q or %q, got %q", ActionAllow, ActionDeny, r.Action)
	}
	p := strings.TrimSpace(r.Pattern)
	if p == "" {
		return fmt.Errorf("rule pattern is required")
	}
	if (r.Tool == ToolWrite || r.Tool == ToolRead) && p != matchAnyRule && !filepath.IsAbs(p) {
		return fmt.Errorf("%s rule pattern must be an absolute directory, got %q", r.Tool, p)
	}
	if r.Tool == ToolMCP && p != matchAnyRule {
		server, tool, hasTool := strings.Cut(p, "/")
		if server == "" || server == matchAnyRule || (hasTool && (tool == "" || strings.Contains(tool, "/"))) {
			return fmt.Errorf("mcp rule pattern must be \"*\", \"server\", \"server/*\" or \"server/tool\", got %q", p)
		}
	}
	return nil
}

func (r Rule) normalized() Rule {
	r.Pattern = strings.TrimSpace(r.Pattern)
	switch {
	case r.Tool == ToolShell:
		r.Pattern = strings.Join(strings.Fields(r.Pattern), " ")
	case r.Tool == ToolMCP:
	case r.Pattern != matchAnyRule:
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
	ReplyGlobal  = "global"
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
	case KindMCP:
		return decideMCP(p, req, rules)
	case KindRead:
		return decideRead(req, rules)
	}
	return Decision{Verdict: Allow}
}

// decideRead: a read outside the workspace runs under a read or write
// allow rule for its real (symlink-resolved) path, else asks with a
// grant for its folder.
func decideRead(req Request, rules []Rule) Decision {
	if r := allowingRead(req.readTarget(), rules); r != nil {
		return Decision{Verdict: Allow, Rule: r}
	}
	return Decision{Verdict: Ask, Reasons: []string{"outside the workspace"},
		Remember: []Rule{readGrant(req.readTarget())}, Choices: writeChoices()}
}

// readGrant is the narrowest useful read rule for t: its folder, or the
// file itself when that folder is too broad to hand out.
// The path is kept as written unless it is a workspace symlink.
func readGrant(t readTarget) Rule {
	path := t.path
	if t.linked {
		path = t.real
	}
	return Rule{Tool: ToolRead, Pattern: narrowFolder(path), Action: ActionAllow}
}

func (req Request) readTarget() readTarget {
	return readTarget{path: req.Path, real: req.Real, linked: req.Linked}
}

// narrowFolder is path's parent unless that is /, a top-level folder,
// the home folder or its parent; then path itself.
func narrowFolder(path string) string {
	dir := filepath.Dir(path)
	home, _ := homeDir()
	broad := dir == "/" || filepath.Dir(dir) == "/"
	if home != "" {
		for _, h := range []string{filepath.Clean(home), tools.RealPath(home)} {
			broad = broad || dir == h || dir == filepath.Dir(h)
		}
	}
	if broad {
		return path
	}
	return dir
}

// allowingRead matches allow-read and allow-write rules against the
// real path, so a symlink cannot borrow another folder's grant.
func allowingRead(t readTarget, rules []Rule) *Rule {
	for i := range rules {
		r := &rules[i]
		if (r.Tool != ToolRead && r.Tool != ToolWrite) || r.Action != ActionAllow {
			continue
		}
		if pathMatches(r.Pattern, t.real) || (r.Pattern != matchAnyRule && pathMatches(tools.RealPath(r.Pattern), t.real)) {
			return r
		}
	}
	return nil
}

// deniesRead matches deny-read rules against either spelling of t.
func deniesRead(r *Rule, t readTarget) bool {
	if r.Tool != ToolRead || r.Action != ActionDeny {
		return false
	}
	if pathMatches(r.Pattern, t.path) || pathMatches(r.Pattern, t.real) {
		return true
	}
	return r.Pattern != matchAnyRule && pathMatches(tools.RealPath(r.Pattern), t.real)
}

// decideMCP: allow rules run the call; destructive tools always ask
// once; anything else asks and can be remembered per tool. A server's
// readOnly hint is not trusted, so it allows nothing.
func decideMCP(p Preset, req Request, rules []Rule) Decision {
	if req.Destructive {
		return Decision{Verdict: Ask, Destructive: true, Reasons: []string{"the server marks this tool destructive"},
			Choices: []string{ReplyOnce, ReplyDeny}}
	}
	if r := allowingMCP(req, rules); r != nil {
		return Decision{Verdict: Allow, Rule: r}
	}
	reason := "MCP tool that may make changes"
	if p == ReadOnly {
		reason = "read-only mode"
	}
	remember := []Rule{{Tool: ToolMCP, Pattern: req.Server + "/" + req.MCPTool, Action: ActionAllow}}
	return Decision{Verdict: Ask, Reasons: []string{reason}, Remember: remember, Choices: allChoices()}
}

func allowingMCP(req Request, rules []Rule) *Rule {
	for i := range rules {
		r := &rules[i]
		if r.Tool == ToolMCP && r.Action == ActionAllow && mcpMatches(r.Pattern, req.Server, req.MCPTool) {
			return r
		}
	}
	return nil
}

func mcpMatches(pattern, server, tool string) bool {
	if pattern == matchAnyRule {
		return true
	}
	ps, pt, hasTool := strings.Cut(pattern, "/")
	if ps != server {
		return false
	}
	return !hasTool || pt == matchAnyRule || pt == tool
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
	return Decision{Verdict: Ask, Reasons: reasons, Remember: remember, Choices: writeChoices()}
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
	if req.Unparsed {
		return Decision{Verdict: Ask, Reasons: []string{"uses shell substitution"},
			Sandbox: sandbox.ModeWorkspaceWrite, Choices: []string{ReplyOnce, ReplyDeny}}
	}
	if req.VarRead {
		return Decision{Verdict: Ask, Reasons: []string{"expands a variable, which may read outside the workspace"},
			Sandbox: safeMode, Choices: []string{ReplyOnce, ReplyDeny}}
	}
	var reads []Rule
	var readReasons []string
	for _, t := range req.Reads {
		if allowingRead(t, rules) != nil {
			continue
		}
		g := readGrant(t)
		if !containsRule(reads, g) {
			reads = append(reads, g)
		}
		readReasons = append(readReasons, "reads "+t.path+" outside the workspace")
	}
	if req.Safe && len(reads) == 0 {
		return Decision{Verdict: Allow, Sandbox: safeMode}
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
	mode := sandbox.ModeWorkspaceWrite
	if req.Safe {
		mode = safeMode
	}
	if len(missing) == 0 && len(reads) == 0 {
		return Decision{Verdict: Allow, Sandbox: mode, Rule: matched}
	}
	var reasons []string
	choices := writeChoices()
	if len(missing) > 0 {
		reasons = append(reasons, "not on the safe list")
		choices = allChoices()
	}
	return Decision{Verdict: Ask, Reasons: append(reasons, readReasons...), Sandbox: mode,
		Remember: append(missing, reads...), Choices: choices}
}

func containsRule(rules []Rule, r Rule) bool {
	for _, x := range rules {
		if x == r {
			return true
		}
	}
	return false
}

func allChoices() []string { return []string{ReplyOnce, ReplySession, ReplyFolder, ReplyDeny} }

// writeChoices adds "everywhere": a folder outside the workspace (a
// tool's install prefix, a config dir) is often shared by every project.
func writeChoices() []string {
	return []string{ReplyOnce, ReplySession, ReplyFolder, ReplyGlobal, ReplyDeny}
}

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
		case KindMCP:
			if r.Tool == ToolMCP && mcpMatches(r.Pattern, req.Server, req.MCPTool) {
				return r
			}
		case KindRead:
			if deniesRead(r, req.readTarget()) {
				return r
			}
		case KindShell:
			for _, t := range req.Reads {
				if deniesRead(r, t) {
					return r
				}
			}
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
