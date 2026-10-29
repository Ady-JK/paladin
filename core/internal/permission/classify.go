package permission

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/Ady-JK/paladin/core/internal/tools"
)

// Request kinds.
const (
	KindShell    = "shell"
	KindWrite    = "write"
	KindEscalate = "escalate"
)

type segment struct {
	words []string
	safe  bool
}

// Request is a gated tool call, classified.
type Request struct {
	Tool        string
	Kind        string
	Command     string
	Path        string
	Outside     bool
	Safe        bool
	Destructive bool
	// Unparsed commands use substitution, so no rule can cover them.
	Unparsed bool
	Segments []segment
}

// Classify reports whether a call needs a permission decision and what
// it would do. Reads, todos, asks and memory never do.
func Classify(name string, raw json.RawMessage, workspace string) (Request, bool) {
	switch name {
	case "shell":
		var a struct {
			Action string `json:"action"`
			Cmd    string `json:"cmd"`
		}
		if json.Unmarshal(raw, &a) != nil || (a.Action != "" && a.Action != "run") || strings.TrimSpace(a.Cmd) == "" {
			return Request{}, false
		}
		return classifyShell(a.Cmd), true
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
		return Request{Tool: name, Kind: KindWrite, Path: path, Outside: scope == tools.ScopeOutside}, true
	}
	return Request{}, false
}

func classifyShell(cmd string) Request {
	req := Request{Tool: "shell", Kind: KindShell, Command: strings.TrimSpace(cmd)}
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
	}
	return req
}

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
