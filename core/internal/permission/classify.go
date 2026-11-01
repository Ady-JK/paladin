package permission

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/Ady-JK/paladin/core/internal/tools"
)

// Request kinds.
const (
	KindShell    = "shell"
	KindWrite    = "write"
	KindEscalate = "escalate"
	KindMCP      = "mcp"
	KindRead     = "read"
	KindWeb      = "web"
	// KindProject asks whether a tool the workspace ships may run.
	KindProject = "project"
	// KindExtension is a call to an extension tool.
	KindExtension = "extension"
)

// maxCardArgs caps the arguments an approval card shows.
const maxCardArgs = 4000

type segment struct {
	words []string
	safe  bool
}

// Request is a gated tool call, classified.
type Request struct {
	Tool    string
	Kind    string
	Command string
	Path    string
	Outside bool
	// Protected is the folder of a write whose files can make something
	// run code later (see protectedDir), or "".
	Protected   string
	Safe        bool
	Destructive bool
	// Real is Path with symlinks resolved (reads); Linked marks a path
	// inside the workspace whose symlink leads out.
	Real   string
	Linked bool
	// Dir marks a read of a whole folder by a search tool.
	Dir bool
	// Unparsed commands use substitution, so no rule can cover them.
	Unparsed bool
	// Hidden marks a command or path with invisible or direction-changing
	// characters: it may not run, or land, where the card shows.
	Hidden bool
	// Reads are paths outside the workspace that otherwise read-only
	// shell segments name; VarRead marks one that expands a variable.
	Reads    []readTarget
	VarRead  bool
	Segments []segment
	// Server, MCPTool and Args describe an MCP call. Destructive carries
	// the server's destructiveHint, which can only make the gate stricter;
	// its readOnlyHint is untrusted and never sets Safe.
	Server  string
	MCPTool string
	Args    string
	// Host and URL describe a web request (web_fetch, browser open).
	Host string
	URL  string
}

// ClassifyMCP describes a call to a tool proxied from an MCP server.
// classifyExtension is a call to an extension tool, with its arguments
// for the card.
func classifyExtension(name string, raw json.RawMessage) Request {
	args := strings.TrimSpace(string(raw))
	if len(args) > maxCardArgs {
		args = args[:maxCardArgs] + "…"
	}
	return Request{Tool: name, Kind: KindExtension, Args: args}
}

func ClassifyMCP(name string, info tools.MCPInfo, raw json.RawMessage) Request {
	args := strings.TrimSpace(string(raw))
	if len(args) > maxCardArgs {
		args = args[:maxCardArgs] + "…"
	}
	return Request{Tool: name, Kind: KindMCP, Server: info.Server, MCPTool: info.Tool, Args: args,
		Destructive: info.Destructive}
}

// Classify reports whether a call needs a permission decision and what
// it would do. Reads, todos, asks and memory never do.
func Classify(name string, raw json.RawMessage, workspace string) (Request, bool) {
	switch name {
	case "shell":
		var a tools.ShellArgs
		if json.Unmarshal(raw, &a) != nil || (a.Action != "" && a.Action != "run") {
			return Request{}, false
		}
		cmd := a.CommandLine()
		if strings.TrimSpace(cmd) == "" {
			return Request{}, false
		}
		return classifyShell(cmd, workspace), true
	case "write", "edit":
		var a struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(raw, &a) != nil {
			return Request{}, false
		}
		path, scope, err := tools.ResolveScopedPath(name, "path", a.Path, workspace)
		if err != nil {
			// The tool rejects it itself; nothing would be written.
			return Request{}, false
		}
		req := Request{Tool: name, Kind: KindWrite, Path: path, Real: tools.RealPath(path), Outside: scope == tools.ScopeOutside,
			Hidden: tools.HiddenChars(a.Path)}
		if !req.Outside {
			// Through a symlink too: link -> .git is still .git.
			req.Protected = protectedDir(workspace, path)
			if req.Protected == "" && protectedDir(tools.RealPath(workspace), tools.RealPath(path)) != "" {
				req.Protected = filepath.Dir(path)
			}
		}
		return req, true
	case "read":
		var a struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(raw, &a) != nil {
			return Request{}, false
		}
		path, scope, err := tools.ResolveScopedPath(name, "path", a.Path, workspace)
		if err != nil || scope == tools.ScopeInside {
			return Request{}, false
		}
		t := newReadTarget(path, workspace)
		return Request{Tool: name, Kind: KindRead, Path: path, Real: t.real, Linked: t.linked, Outside: true}, true
	case "grep", "glob", "list":
		return classifySearch(name, raw, workspace)
	case "web_fetch", "browser":
		return classifyWeb(name, raw)
	}
	return Request{}, false
}

// classifyWeb gates web_fetch and browser open by site, and a service on
// this machine by port (localhost:PORT, whichever name the URL used).
// Invalid URLs (the tool rejects them) are not gated; other browser
// actions stay on the page already approved.
func classifyWeb(name string, raw json.RawMessage) (Request, bool) {
	var a struct {
		Action string `json:"action"`
		URL    string `json:"url"`
	}
	if json.Unmarshal(raw, &a) != nil || (name == "browser" && a.Action != "open") {
		return Request{}, false
	}
	u, err := tools.ParseWebURL(a.URL)
	if err != nil {
		return Request{}, false
	}
	host := strings.ToLower(u.Hostname())
	if tools.LocalHost(host) {
		port := u.Port()
		if port == "" {
			port = map[string]string{"http": "80", "https": "443"}[u.Scheme]
		}
		host = "localhost:" + port
	}
	return Request{Tool: name, Kind: KindWeb, Host: host, URL: u.String()}, true
}

// classifySearch gates a search whose root lies outside the workspace
// like a read of that folder (or file). No path means the workspace.
func classifySearch(name string, raw json.RawMessage, workspace string) (Request, bool) {
	var a struct {
		Path string `json:"path"`
	}
	if json.Unmarshal(raw, &a) != nil || strings.TrimSpace(a.Path) == "" {
		return Request{}, false
	}
	path, scope, err := tools.ResolveScopedPath(name, "path", a.Path, workspace)
	if err != nil || scope == tools.ScopeInside {
		return Request{}, false
	}
	t := newReadTarget(path, workspace)
	info, err := os.Stat(path)
	return Request{Tool: name, Kind: KindRead, Path: path, Real: t.real, Linked: t.linked, Outside: true,
		Dir: err == nil && info.IsDir()}, true
}

func classifyShell(cmd, workspace string) Request {
	req := Request{Tool: "shell", Kind: KindShell, Command: strings.TrimSpace(cmd), Hidden: tools.HiddenChars(cmd)}
	req.Safe = tools.ReadOnlyCommand(cmd) == nil
	req.Destructive = isDestructive(cmd)
	req.Unparsed = strings.Contains(cmd, "$(") || strings.Contains(cmd, "`") ||
		strings.Contains(cmd, "<(") || strings.Contains(cmd, ">(")
	segs, err := splitCommand(cmd)
	if err != nil {
		req.Unparsed = true
		return req
	}
	for _, words := range segs {
		safe := !redirects(words) && tools.ReadOnlyCommand(strings.Join(quoteAll(words), " ")) == nil
		req.Segments = append(req.Segments, segment{words: words, safe: safe})
		// A bare cd (or cd -) goes to a folder the words do not name, so
		// relative paths after it may be outside the workspace.
		if words[0] == "cd" && (len(words) == 1 || words[1] == "-" || words[1] == "--") {
			req.VarRead = true
		}
		if safe {
			reads, variable := outsideReads(words, workspace)
			req.Reads = append(req.Reads, reads...)
			req.VarRead = req.VarRead || variable
		}
	}
	return req
}

// readTarget is a path a command reads: as written and symlink-resolved.
// linked marks a path inside the workspace whose symlink leads out; dir
// marks a folder searched as a whole (grep, glob, list).
type readTarget struct {
	path, real string
	linked     bool
	dir        bool
}

func newReadTarget(path, workspace string) readTarget {
	return readTarget{path: path, real: tools.RealPath(path), linked: tools.Within(path, workspace)}
}

// outsideReads finds the arguments of a read-only command that name a
// path outside the workspace: absolute, ~, .. or an existing symlink
// that leads out. Best effort — a program can still open paths it does
// not take as arguments. variable reports a $ the shell would expand.
func outsideReads(words []string, workspace string) (out []readTarget, variable bool) {
	home, _ := homeDir()
	for _, w := range words[1:] {
		if strings.HasPrefix(w, "-") {
			_, v, ok := strings.Cut(w, "=")
			if !ok {
				continue
			}
			w = v
		}
		if strings.Contains(w, "$") || (strings.HasPrefix(w, "~") && w != "~" && !strings.HasPrefix(w, "~/")) {
			variable = true
			continue
		}
		var abs string
		switch {
		case w == "~" || strings.HasPrefix(w, "~/"):
			if home == "" {
				variable = true
				continue
			}
			abs = filepath.Join(home, strings.TrimPrefix(w, "~"))
		case filepath.IsAbs(w):
			abs = w
		case workspace != "":
			abs = filepath.Join(workspace, w)
		default:
			continue
		}
		abs = filepath.Clean(abs)
		if safeDevice[abs] {
			continue
		}
		if !filepath.IsAbs(w) && !strings.HasPrefix(w, "~") && !strings.Contains(w, "..") {
			// Plain relative words (patterns, file names) only count
			// when they exist and resolve out of the workspace.
			if _, err := os.Lstat(abs); err != nil {
				continue
			}
		}
		if _, scope, err := tools.ResolveScopedPath("shell", "path", abs, workspace); err == nil && scope == tools.ScopeOutside {
			out = append(out, newReadTarget(abs, workspace))
		}
	}
	return out, variable
}

var safeDevice = map[string]bool{"/dev/null": true, "/dev/stdin": true, "/dev/stdout": true, "/dev/stderr": true}

// redirects reports an output redirection; the words were unquoted, so
// a quoted ">" argument is over-reported, which only costs a prompt.
func redirects(words []string) bool {
	for _, w := range words {
		if strings.Contains(w, ">") {
			return true
		}
	}
	return false
}

func quoteAll(words []string) []string {
	out := make([]string, len(words))
	for i, w := range words {
		out[i] = "'" + strings.ReplaceAll(w, "'", `'\''`) + "'"
	}
	return out
}

// splitCommand splits on ;, &, &&, ||, | and newlines outside quotes and
// returns each segment's words, quotes removed. Redirections stay as words.
// A # starting a word begins a comment to the end of the line, as in the
// shell: a quote inside it must not swallow the lines after it.
func splitCommand(cmd string) ([][]string, error) {
	var (
		segs  [][]string
		words []string
		cur   strings.Builder
		has   bool
		quote rune
	)
	endWord := func() {
		if has || cur.Len() > 0 {
			words = append(words, cur.String())
		}
		cur.Reset()
		has = false
	}
	endSeg := func() {
		endWord()
		if len(words) > 0 {
			segs = append(segs, words)
		}
		words = nil
	}
	runes := []rune(cmd)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if quote != 0 {
			switch {
			case r == quote:
				quote = 0
			case r == '\\' && quote == '"' && i+1 < len(runes):
				i++
				cur.WriteRune(runes[i])
			default:
				cur.WriteRune(r)
			}
			continue
		}
		switch r {
		case '#':
			if cur.Len() == 0 && !has {
				for i+1 < len(runes) && runes[i+1] != '\n' {
					i++
				}
				continue
			}
			cur.WriteRune(r)
		case '\'', '"':
			quote, has = r, true
		case '\\':
			if i+1 < len(runes) {
				i++
				if runes[i] != '\n' {
					cur.WriteRune(runes[i])
				}
			}
		case ' ', '\t':
			endWord()
		case ';', '\n', '|', '&':
			// >& and &> are redirections, not separators.
			if r == '&' && ((i > 0 && runes[i-1] == '>') || (i+1 < len(runes) && runes[i+1] == '>')) {
				cur.WriteRune(r)
				continue
			}
			endSeg()
			if (r == '|' || r == '&') && i+1 < len(runes) && runes[i+1] == r {
				i++
			}
		case '(', ')', '{', '}':
			endSeg()
		default:
			cur.WriteRune(r)
		}
	}
	if quote != 0 {
		return nil, errUnbalanced
	}
	endSeg()
	return segs, nil
}

type parseError string

func (e parseError) Error() string { return string(e) }

const errUnbalanced = parseError("unbalanced quotes")

// normalizeWords drops leading VAR=value words and reduces the program
// to its base name, so "FOO=1 /usr/bin/make all" matches "make all".
func normalizeWords(words []string) []string {
	for len(words) > 0 && isAssign(words[0]) {
		words = words[1:]
	}
	if len(words) == 0 {
		return nil
	}
	out := append([]string{filepath.Base(words[0])}, words[1:]...)
	// Wrappers that just run the next command are looked through.
	for len(out) > 1 && (out[0] == "sudo" || out[0] == "doas" || out[0] == "env" || out[0] == "nohup" || out[0] == "time" || out[0] == "xargs" || out[0] == "exec" || out[0] == "command") {
		if out[0] == "sudo" || out[0] == "doas" {
			break
		}
		rest := out[1:]
		for len(rest) > 0 && (strings.HasPrefix(rest[0], "-") || isAssign(rest[0])) {
			rest = rest[1:]
		}
		if len(rest) == 0 {
			break
		}
		out = append([]string{filepath.Base(rest[0])}, rest[1:]...)
	}
	return out
}

func isAssign(w string) bool {
	i := strings.IndexByte(w, '=')
	return i > 0 && !strings.ContainsAny(w[:i], "/-.")
}

// prefixOf is the rule a "remember" answer saves: the program plus its
// subcommand when the second word looks like one ("npm test", "go build").
func prefixOf(words []string) string {
	words = normalizeWords(words)
	if len(words) == 0 {
		return ""
	}
	if len(words) > 1 && isSubcommand(words[1]) {
		return words[0] + " " + words[1]
	}
	return words[0]
}

func isSubcommand(w string) bool {
	if w == "" || strings.HasPrefix(w, "-") || strings.ContainsAny(w, "/.=:@~$*?") {
		return false
	}
	for _, r := range w {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// isDestructive flags commands that can lose work beyond a sandbox's
// reach (force pushes, history rewrites) or wipe trees. It errs toward
// asking; it is a prompt trigger, not a security boundary.
func isDestructive(cmd string) bool {
	segs, err := splitCommand(cmd)
	if err != nil {
		return strings.Contains(cmd, "rm -")
	}
	for _, raw := range segs {
		w := normalizeWords(raw)
		if len(w) == 0 {
			continue
		}
		if destructiveSegment(w) {
			return true
		}
	}
	return false
}

func destructiveSegment(w []string) bool {
	prog, args := w[0], w[1:]
	switch {
	case prog == "sudo" || prog == "doas" || prog == "su":
		return true
	case prog == "rm":
		for _, a := range args {
			if a == "--recursive" || a == "--force" || (strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.ContainsAny(a, "rRf")) {
				return true
			}
		}
	case prog == "shred" || prog == "dd" || strings.HasPrefix(prog, "mkfs") || prog == "fdisk" || prog == "diskutil" || prog == "wipefs":
		return true
	case prog == "chmod" || prog == "chown" || prog == "chgrp":
		return hasFlag(args, "-R", "--recursive")
	case prog == "find":
		return hasFlag(args, "-delete") || (hasFlag(args, "-exec", "-execdir") && contains(args, "rm"))
	case prog == "git":
		return destructiveGit(args)
	}
	return false
}

func destructiveGit(args []string) bool {
	sub, rest := "", []string(nil)
	for i, a := range args {
		if !strings.HasPrefix(a, "-") {
			sub, rest = a, args[i+1:]
			break
		}
	}
	switch sub {
	case "push":
		return hasFlag(rest, "-f", "--force", "--force-with-lease", "--mirror", "--delete", "-d") || anyPrefix(rest, "+", "--force")
	case "reset":
		return hasFlag(rest, "--hard", "--merge", "--keep")
	case "clean":
		for _, a := range rest {
			if a == "--force" || (strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "f")) {
				return true
			}
		}
	case "checkout", "restore":
		return contains(rest, "--") || contains(rest, ".") || hasFlag(rest, "-f", "--force")
	case "branch":
		return hasFlag(rest, "-D") || (hasFlag(rest, "-d", "--delete") && hasFlag(rest, "-f", "--force"))
	case "stash":
		return len(rest) > 0 && (rest[0] == "drop" || rest[0] == "clear")
	case "filter-branch", "filter-repo":
		return true
	case "rebase":
		return false
	}
	return false
}

func hasFlag(args []string, flags ...string) bool {
	for _, a := range args {
		for _, f := range flags {
			if a == f || strings.HasPrefix(a, f+"=") {
				return true
			}
		}
	}
	return false
}

func anyPrefix(args []string, prefixes ...string) bool {
	for _, a := range args {
		for _, p := range prefixes {
			if strings.HasPrefix(a, p) {
				return true
			}
		}
	}
	return false
}

func contains(args []string, s string) bool {
	for _, a := range args {
		if a == s {
			return true
		}
	}
	return false
}

// protectedDir is the folder of path, below base, whose files make
// something run code later: git's config and hooks (.git), agent
// settings, hooks and MCP servers (.paladin, .claude), and installed
// tools and packages Paladin or the user runs (node_modules, a Python
// virtualenv). It is "" for any other path. Names compare case-blind,
// as on macOS's default file system.
func protectedDir(base, path string) string {
	rel, err := filepath.Rel(base, path)
	if base == "" || err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	cur := base
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		cur = filepath.Join(cur, part)
		switch strings.ToLower(part) {
		case ".git", ".paladin", ".claude", "node_modules", ".venv":
			return cur
		}
		if _, err := os.Stat(filepath.Join(cur, "pyvenv.cfg")); err == nil {
			return cur
		}
	}
	return ""
}
