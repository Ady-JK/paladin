// plan.go is the read-only guard for plan mode. The
// model may inspect the workspace (list, read, search, git history) but
// any command that could change state is refused before it runs.

package tools

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// readOnlyPrograms may run in plan mode. Programs with dangerous flags
// are further checked in readOnlyArgs. Environment and config dumpers
// (env, printenv, go env, git config) are left out: they print secrets.
var readOnlyPrograms = map[string]bool{
	"ls": true, "cat": true, "head": true, "tail": true, "wc": true, "pwd": true,
	"grep": true, "rg": true, "egrep": true, "fgrep": true, "ag": true, "find": true, "fd": true,
	"tree": true, "file": true, "stat": true, "du": true, "df": true, "which": true, "whereis": true,
	"type": true, "echo": true, "printf": true, "uname": true,
	"date": true, "whoami": true, "id": true, "basename": true, "dirname": true, "realpath": true,
	"readlink": true, "sort": true, "uniq": true, "cut": true, "tr": true, "diff": true, "cmp": true,
	"jq": true, "sed": true, "awk": true, "less": true, "more": true, "nl": true, "column": true,
	"md5": true, "md5sum": true, "shasum": true, "sha256sum": true, "true": true, "false": true,
	"test": true, "[": true, "cd": true, "git": true, "go": true, "xxd": true, "hexdump": true,
}

var readOnlyGit = map[string]bool{
	"status": true, "diff": true, "log": true, "show": true, "blame": true, "branch": true,
	"rev-parse": true, "ls-files": true, "grep": true, "remote": true, "describe": true,
	"shortlog": true, "tag": true, "reflog": true, "cat-file": true, "ls-tree": true,
}

var readOnlyGo = map[string]bool{"list": true, "version": true, "doc": true, "vet": true}

// ReadOnlyProgramList lists the program names plan mode allows, sorted.
// The subagent prompt names the same set, and a test keeps them equal.
func ReadOnlyProgramList() []string {
	out := make([]string, 0, len(readOnlyPrograms))
	for name := range readOnlyPrograms {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// ShellReadOnly reports whether a shell call cannot change the
// workspace: a foreground run the plan-mode allowlist accepts, or a
// status/cancel that only talks to job bookkeeping. Undo snapshots skip
// these, which avoids two whole-folder git scans per `ls` or `rg`.
func ShellReadOnly(raw string) bool {
	var args ShellArgs
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return false
	}
	if args.Action == ShellStatus || args.Action == ShellCancel {
		return true
	}
	if args.Action != "" && args.Action != ShellRun {
		return false
	}
	if args.Background {
		return false
	}
	cmd := strings.TrimSpace(args.CommandLine())
	return cmd != "" && ReadOnlyCommand(cmd) == nil
}

// ReadOnlyCommand reports why cmd is not safe in plan mode, or nil.
// It is a conservative allowlist: anything it does not understand is
// refused, and the model is told to ask the user to switch to build mode.
func ReadOnlyCommand(cmd string) error {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return fmt.Errorf("empty command")
	}
	if strings.Contains(cmd, "$(") || strings.Contains(cmd, "`") || strings.Contains(cmd, "<(") || strings.Contains(cmd, ">(") {
		return fmt.Errorf("command substitution is not allowed")
	}
	if HiddenChars(cmd) {
		return fmt.Errorf("the command has invisible or direction-changing characters")
	}
	segments, err := splitShellSegments(cmd)
	if err != nil {
		return err
	}
	for _, seg := range segments {
		words := shellWords(seg)
		if len(words) > 0 && isEnvAssign(words[0]) {
			// GIT_CONFIG_*, LD_PRELOAD, PAGER and the like make a
			// reader run code.
			return fmt.Errorf("environment assignments are not allowed in read-only commands")
		}
		if len(words) == 0 {
			continue
		}
		if err := readOnlyArgs(words); err != nil {
			return err
		}
	}
	return nil
}

// splitShellSegments splits on ;, &&, ||, | and newlines outside quotes
// and rejects output redirection (other than to /dev/null or another
// descriptor) and comments: the shell ignores a quote inside a comment,
// so a parser that does not would read the next line as quoted text.
func splitShellSegments(cmd string) ([]string, error) {
	var (
		segs  []string
		cur   strings.Builder
		quote rune
	)
	runes := []rune(cmd)
	flush := func() {
		segs = append(segs, cur.String())
		cur.Reset()
	}
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if quote != 0 {
			if r == quote {
				quote = 0
			} else if r == '\\' && quote == '"' && i+1 < len(runes) {
				cur.WriteRune(r)
				i++
				r = runes[i]
			}
			cur.WriteRune(r)
			continue
		}
		switch r {
		case '#':
			if i == 0 || strings.ContainsRune(" \t\n;|&()", runes[i-1]) {
				return nil, fmt.Errorf("comments are not allowed in read-only commands")
			}
			cur.WriteRune(r)
		case '\'', '"':
			quote = r
			cur.WriteRune(r)
		case '\\':
			cur.WriteRune(r)
			if i+1 < len(runes) {
				i++
				cur.WriteRune(runes[i])
			}
		case ';', '\n', '|':
			flush()
			if r == '|' && i+1 < len(runes) && runes[i+1] == '|' {
				i++
			}
		case '&':
			if i+1 < len(runes) && runes[i+1] == '&' {
				i++
				flush()
			} else if i > 0 && runes[i-1] == '>' {
				cur.WriteRune(r)
			} else {
				return nil, fmt.Errorf("background operator & is not allowed")
			}
		case '>':
			if !harmlessRedirect.MatchString(string(runes[i+1:])) {
				return nil, fmt.Errorf("output redirection is not allowed")
			}
			cur.WriteRune(r)
		default:
			cur.WriteRune(r)
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unbalanced quotes")
	}
	flush()
	return segs, nil
}

// shellWords splits a segment into words, dropping quotes.
func shellWords(seg string) []string {
	var (
		words []string
		cur   strings.Builder
		quote rune
		has   bool
	)
	for _, r := range seg {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote, has = r, true
		case r == ' ' || r == '\t':
			if has || cur.Len() > 0 {
				words = append(words, cur.String())
				cur.Reset()
				has = false
			}
		default:
			cur.WriteRune(r)
		}
	}
	if has || cur.Len() > 0 {
		words = append(words, cur.String())
	}
	return words
}

// HiddenChars reports invisible or direction-changing characters
// (Unicode format characters such as zero-width spaces and bidi
// overrides, and control characters other than tab and newline). They
// can make text read differently from what runs or gets written.
func HiddenChars(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Cf, r) || (unicode.IsControl(r) && r != '\t' && r != '\n' && r != '\r') {
			return true
		}
	}
	return false
}

// harmlessRedirect is what may follow ">": /dev/null (also as >>), or
// another descriptor (>&2, >&-). ">&file" writes the file.
var harmlessRedirect = regexp.MustCompile(`^>?(\s*/dev/null|&(\d+|-))(\s|$|[;|&)])`)

func isEnvAssign(w string) bool {
	i := strings.IndexByte(w, '=')
	return i > 0 && !strings.ContainsAny(w[:i], "/-.")
}

func readOnlyArgs(words []string) error {
	prog := words[0]
	if i := strings.LastIndexByte(prog, '/'); i >= 0 {
		prog = prog[i+1:]
	}
	if !readOnlyPrograms[prog] {
		return fmt.Errorf("%q is not a read-only command", words[0])
	}
	args := words[1:]
	switch prog {
	case "git":
		sub := ""
		for _, a := range args {
			if !strings.HasPrefix(a, "-") {
				sub = a
				break
			}
		}
		if !readOnlyGit[sub] {
			return fmt.Errorf("git %s may change the repository", sub)
		}
		if err := gitReadOnly(sub, args); err != nil {
			return err
		}
	case "go":
		if len(args) == 0 || !readOnlyGo[args[0]] {
			return fmt.Errorf("go %s is not read-only", strings.Join(args, " "))
		}
		for _, a := range args {
			if hasAnyPrefix(strings.TrimPrefix(a, "-"), "-toolexec", "-vettool", "-exec", "toolexec", "vettool", "exec") && strings.HasPrefix(a, "-") {
				return fmt.Errorf("go %s runs another program", a)
			}
		}
	case "sed":
		if err := sedReadOnly(args); err != nil {
			return err
		}
	case "find":
		for _, a := range args {
			switch a {
			case "-delete", "-exec", "-execdir", "-ok", "-okdir", "-fprint", "-fprint0", "-fprintf", "-fls":
				return fmt.Errorf("find %s is not read-only", a)
			}
		}
	case "awk":
		for _, a := range args {
			if a == "-f" || a == "-E" || a == "-i" || a == "-l" || hasAnyPrefix(a, "--file", "--exec", "--include", "--load") {
				return fmt.Errorf("awk %s loads a program from a file", a)
			}
			if strings.Contains(a, "system") || strings.Contains(a, "getline") || awkPipe.MatchString(a) || awkPrint.MatchString(a) {
				return fmt.Errorf("awk program may run commands or write files")
			}
			if strings.Contains(a, "ENVIRON") {
				return fmt.Errorf("awk ENVIRON reads the environment")
			}
		}
	case "rg":
		for _, a := range args {
			if a == "--pre" || strings.HasPrefix(a, "--pre=") {
				return fmt.Errorf("rg --pre runs a program on each file")
			}
		}
	case "fd":
		for _, a := range args {
			if hasAnyPrefix(a, "--exec") || (strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.ContainsAny(a, "xX")) {
				return fmt.Errorf("fd %s runs a program on each match", a)
			}
		}
	case "ag":
		for _, a := range args {
			if hasAnyPrefix(a, "--pager") {
				return fmt.Errorf("ag --pager runs a program")
			}
		}
	case "sort":
		for _, a := range args {
			if hasAnyPrefix(a, "--output", "--compress-program") || (strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "o")) {
				return fmt.Errorf("sort %s writes a file or runs a program", a)
			}
		}
	case "uniq", "xxd":
		if len(positional(args)) > 1 {
			return fmt.Errorf("%s with a second file writes it", prog)
		}
	case "tree":
		for _, a := range args {
			if a == "-o" || strings.HasPrefix(a, "-o") && !strings.HasPrefix(a, "--") {
				return fmt.Errorf("tree -o writes a file")
			}
		}
	case "less", "more":
		for _, a := range args {
			if hasAnyPrefix(a, "-o", "-O", "--log-file", "--LOG-FILE") {
				return fmt.Errorf("%s %s writes a log file", prog, a)
			}
		}
	case "file":
		for _, a := range args {
			if a == "-C" || a == "--compile" {
				return fmt.Errorf("file -C writes a file")
			}
		}
	case "jq":
		for _, a := range args {
			if strings.Contains(a, "$ENV") || strings.Contains(a, "env") {
				return fmt.Errorf("jq env reads the environment")
			}
		}
	}
	return nil
}

// PlanShell wraps the shell tool for plan mode.
type PlanShell struct{ inner *ShellTool }

func NewPlanShell(inner *ShellTool) *PlanShell { return &PlanShell{inner: inner} }

func (*PlanShell) Name() string { return "shell" }

func (p *PlanShell) Definition() ToolDefinition {
	def := p.inner.Definition()
	def.Description = "PLAN MODE (read-only): only inspection commands supported on this OS run, " +
		"such as git status/diff/log/show. Writes, redirection, installs " +
		"and builds are refused. " + def.Description
	return def
}

func (p *PlanShell) Call(raw json.RawMessage, ctx *ToolCtx) (string, error) {
	var args ShellArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return p.inner.Call(raw, ctx)
	}
	if args.Action == "" || args.Action == ShellRun {
		if err := ReadOnlyCommand(args.CommandLine()); err != nil {
			return "", ArgsError(fmt.Sprintf("plan mode: %v. Plan mode is read-only; describe the change in your plan, "+
				"and the user can switch to build mode to carry it out.", err))
		}
	}
	return p.inner.Call(raw, ctx)
}

// awkPipe is a single | (a pipe to or from a command; || is or).
var awkPipe = regexp.MustCompile(`(^|[^|])\|([^|]|$)`)

// awkPrint is print or printf with output redirection.
var awkPrint = regexp.MustCompile(`\bprintf?\b[^;{}\n]*>`)

func hasAnyPrefix(s string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// positional is args without options.
func positional(args []string) []string {
	var out []string
	for _, a := range args {
		if a != "-" && !strings.HasPrefix(a, "-") {
			out = append(out, a)
		}
	}
	return out
}

// gitListing are options that make branch and tag list (their other
// arguments are patterns or commits, not names to create).
var gitListing = map[string]bool{"-l": true, "--list": true, "--contains": true, "--no-contains": true,
	"--merged": true, "--no-merged": true, "--points-at": true}

// gitChanges are the arguments that make a listing subcommand change
// refs or remotes.
var gitChanges = map[string]map[string]bool{
	"branch": wordSet("-d", "-D", "-m", "-M", "-c", "-C", "-u", "-f", "--copy", "--delete", "--move", "--force",
		"--set-upstream-to", "--unset-upstream", "--edit-description", "--track", "--create-reflog"),
	"tag":    wordSet("-d", "--delete", "-f", "--force", "-a", "-s", "-u", "-m", "-F", "--annotate", "--sign"),
	"remote": wordSet("add", "remove", "rm", "set-url", "rename", "prune", "update", "set-head", "set-branches"),
	"reflog": wordSet("expire", "delete"),
}

func wordSet(words ...string) map[string]bool {
	m := make(map[string]bool, len(words))
	for _, w := range words {
		m[w] = true
	}
	return m
}

// gitReadOnly refuses git options that run programs, write files or
// change refs, for the read-only subcommands.
func gitReadOnly(sub string, args []string) error {
	for _, a := range args {
		if a == sub {
			break
		}
		if a == "-c" || hasAnyPrefix(a, "--config-env", "--exec-path") {
			return fmt.Errorf("git %s sets configuration that can run programs", a)
		}
	}
	listing := false
	for _, a := range args {
		if hasAnyPrefix(a, "--output", "--open-files-in-pager", "--ext-diff") || (sub == "grep" && strings.HasPrefix(a, "-O")) {
			return fmt.Errorf("git %s %s writes a file or runs a program", sub, a)
		}
		if gitListing[strings.SplitN(a, "=", 2)[0]] {
			listing = true
		}
	}
	after := false
	for _, a := range args {
		if !after {
			after = a == sub
			continue
		}
		if gitChanges[sub][a] {
			return fmt.Errorf("git %s %s changes the repository", sub, a)
		}
		if (sub == "branch" || sub == "tag") && !listing && !strings.HasPrefix(a, "-") {
			return fmt.Errorf("git %s %s creates a %s", sub, a, sub)
		}
	}
	return nil
}

// sedReadOnly refuses in-place edits, script files and scripts that
// write files or run commands.
func sedReadOnly(args []string) error {
	var scripts, files []string
	explicit := false
	for j := 0; j < len(args); j++ {
		a := args[j]
		switch {
		case a == "--in-place" || strings.HasPrefix(a, "--in-place="):
			return fmt.Errorf("sed -i edits files")
		case a == "--file" || strings.HasPrefix(a, "--file="):
			return fmt.Errorf("sed -f runs a script from a file")
		case a == "--expression" || strings.HasPrefix(a, "--expression="):
			explicit = true
			if v, ok := strings.CutPrefix(a, "--expression="); ok {
				scripts = append(scripts, v)
			} else if j+1 < len(args) {
				j++
				scripts = append(scripts, args[j])
			}
		case strings.HasPrefix(a, "--"):
		case len(a) > 1 && a[0] == '-':
			for k := 1; k < len(a); k++ {
				switch a[k] {
				case 'i', 'I':
					return fmt.Errorf("sed -i edits files")
				case 'f':
					return fmt.Errorf("sed -f runs a script from a file")
				case 'e':
					explicit = true
					if k+1 < len(a) {
						scripts = append(scripts, a[k+1:])
					} else if j+1 < len(args) {
						j++
						scripts = append(scripts, args[j])
					}
					k = len(a)
				}
			}
		default:
			files = append(files, a)
		}
	}
	if !explicit && len(files) > 0 {
		scripts = append(scripts, files[0])
	}
	for _, sc := range scripts {
		if sedScriptUnsafe(sc) {
			return fmt.Errorf("sed script may write files or run commands")
		}
	}
	return nil
}

// sedScriptUnsafe reports a script with the w, W, r, R or e commands or
// the s command's w or e flags. A script it cannot follow counts as
// unsafe.
func sedScriptUnsafe(s string) bool {
	i, n := 0, len(s)
	addr := func() bool {
		switch {
		case i < n && s[i] >= '0' && s[i] <= '9':
			for i < n && (s[i] >= '0' && s[i] <= '9' || s[i] == '~') {
				i++
			}
		case i < n && s[i] == '$':
			i++
		case i < n && (s[i] == '/' || s[i] == '\\'):
			delim := byte('/')
			if s[i] == '\\' {
				i++
				if i >= n {
					return false
				}
				delim = s[i]
			}
			i++
			for i < n && s[i] != delim {
				if s[i] == '\\' {
					i++
				}
				i++
			}
			if i >= n {
				return false
			}
			i++
			for i < n && (s[i] == 'I' || s[i] == 'M') {
				i++
			}
		}
		return true
	}
	for {
		for i < n && strings.IndexByte(" \t\n;{}!", s[i]) >= 0 {
			i++
		}
		if i >= n {
			return false
		}
		if !addr() {
			return true
		}
		for i < n && s[i] == ' ' {
			i++
		}
		if i < n && s[i] == ',' {
			i++
			for i < n && s[i] == ' ' {
				i++
			}
			if !addr() {
				return true
			}
		}
		for i < n && (s[i] == ' ' || s[i] == '!') {
			i++
		}
		if i >= n {
			return false
		}
		c := s[i]
		i++
		switch c {
		case 'e', 'w', 'W', 'r', 'R':
			return true
		case 's', 'y':
			if i >= n || s[i] == '\\' || s[i] == '\n' {
				return true
			}
			delim := s[i]
			i++
			for part := 0; part < 2; part++ {
				for i < n && s[i] != delim {
					if s[i] == '\\' {
						i++
					}
					i++
				}
				if i >= n {
					return true
				}
				i++
			}
			for c == 's' && i < n && strings.IndexByte("gpiImMew0123456789", s[i]) >= 0 {
				if s[i] == 'e' || s[i] == 'w' {
					return true
				}
				i++
			}
		case 'a', 'i', 'c', '#':
			for i < n && s[i] != '\n' {
				i++
			}
		case ':', 'b', 't', 'T', 'q', 'Q', 'l', 'L':
			for i < n && s[i] != '\n' && s[i] != ';' && s[i] != '}' {
				i++
			}
		case '{', '}', 'p', 'P', 'd', 'D', 'n', 'N', 'g', 'G', 'h', 'H', 'x', '=', 'z', 'F':
		default:
			return true
		}
	}
}
