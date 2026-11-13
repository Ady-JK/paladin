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
func splitShellSegments(command string) ([]string, error) {
	parser := shellSegmentParser{runes: []rune(command)}
	for parser.index = 0; parser.index < len(parser.runes); parser.index++ {
		if parser.quote != 0 {
			parser.consumeQuoted()
			continue
		}
		if err := parser.consumeUnquoted(); err != nil {
			return nil, err
		}
	}
	if parser.quote != 0 {
		return nil, fmt.Errorf("unbalanced quotes")
	}
	parser.flush()
	return parser.segments, nil
}

type shellSegmentParser struct {
	runes    []rune
	index    int
	quote    rune
	current  strings.Builder
	segments []string
}

func (p *shellSegmentParser) flush() {
	p.segments = append(p.segments, p.current.String())
	p.current.Reset()
}

func (p *shellSegmentParser) consumeQuoted() {
	current := p.runes[p.index]
	if current == p.quote {
		p.quote = 0
	} else if current == '\\' && p.quote == '"' && p.index+1 < len(p.runes) {
		p.current.WriteRune(current)
		p.index++
		current = p.runes[p.index]
	}
	p.current.WriteRune(current)
}

func (p *shellSegmentParser) consumeUnquoted() error {
	current := p.runes[p.index]
	switch current {
	case '#':
		if p.index == 0 || strings.ContainsRune(" \t\n;|&()", p.runes[p.index-1]) {
			return fmt.Errorf("comments are not allowed in read-only commands")
		}
		p.current.WriteRune(current)
	case '\'', '"':
		p.quote = current
		p.current.WriteRune(current)
	case '\\':
		p.current.WriteRune(current)
		if p.index+1 < len(p.runes) {
			p.index++
			p.current.WriteRune(p.runes[p.index])
		}
	case ';', '\n', '|':
		p.flush()
		if current == '|' && p.index+1 < len(p.runes) && p.runes[p.index+1] == '|' {
			p.index++
		}
	case '&':
		return p.consumeAmpersand()
	case '>':
		if !harmlessRedirect.MatchString(string(p.runes[p.index+1:])) {
			return fmt.Errorf("output redirection is not allowed")
		}
		p.current.WriteRune(current)
	default:
		p.current.WriteRune(current)
	}
	return nil
}

func (p *shellSegmentParser) consumeAmpersand() error {
	if p.index+1 < len(p.runes) && p.runes[p.index+1] == '&' {
		p.index++
		p.flush()
		return nil
	}
	if p.index > 0 && p.runes[p.index-1] == '>' {
		p.current.WriteRune('&')
		return nil
	}
	return fmt.Errorf("background operator & is not allowed")
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
		return readOnlyGitArgs(args)
	case "go":
		return readOnlyGoArgs(args)
	case "sed":
		return sedReadOnly(args)
	case "uniq", "xxd":
		if len(positional(args)) > 1 {
			return fmt.Errorf("%s with a second file writes it", prog)
		}
		return nil
	}
	for _, arg := range args {
		if err := readOnlyExecutionArg(prog, arg); err != nil {
			return err
		}
		if err := readOnlyOutputArg(prog, arg); err != nil {
			return err
		}
	}
	return nil
}

func readOnlyGitArgs(args []string) error {
	subcommand := ""
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			subcommand = arg
			break
		}
	}
	if !readOnlyGit[subcommand] {
		return fmt.Errorf("git %s may change the repository", subcommand)
	}
	return gitReadOnly(subcommand, args)
}

func readOnlyGoArgs(args []string) error {
	if len(args) == 0 || !readOnlyGo[args[0]] {
		return fmt.Errorf("go %s is not read-only", strings.Join(args, " "))
	}
	for _, arg := range args {
		if hasAnyPrefix(strings.TrimPrefix(arg, "-"), "-toolexec", "-vettool", "-exec", "toolexec", "vettool", "exec") && strings.HasPrefix(arg, "-") {
			return fmt.Errorf("go %s runs another program", arg)
		}
	}
	return nil
}

func readOnlyExecutionArg(program, arg string) error {
	switch program {
	case "find":
		switch arg {
		case "-delete", "-exec", "-execdir", "-ok", "-okdir", "-fprint", "-fprint0", "-fprintf", "-fls":
			return fmt.Errorf("find %s is not read-only", arg)
		}
	case "awk":
		return readOnlyAwkArg(arg)
	case "rg":
		if arg == "--pre" || strings.HasPrefix(arg, "--pre=") {
			return fmt.Errorf("rg --pre runs a program on each file")
		}
	case "fd":
		if hasAnyPrefix(arg, "--exec") || (strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") && strings.ContainsAny(arg, "xX")) {
			return fmt.Errorf("fd %s runs a program on each match", arg)
		}
	case "ag":
		if hasAnyPrefix(arg, "--pager") {
			return fmt.Errorf("ag --pager runs a program")
		}
	}
	return nil
}

func readOnlyAwkArg(arg string) error {
	if arg == "-f" || arg == "-E" || arg == "-i" || arg == "-l" || hasAnyPrefix(arg, "--file", "--exec", "--include", "--load") {
		return fmt.Errorf("awk %s loads a program from a file", arg)
	}
	if strings.Contains(arg, "system") || strings.Contains(arg, "getline") || awkPipe.MatchString(arg) || awkPrint.MatchString(arg) {
		return fmt.Errorf("awk program may run commands or write files")
	}
	if strings.Contains(arg, "ENVIRON") {
		return fmt.Errorf("awk ENVIRON reads the environment")
	}
	return nil
}

func readOnlyOutputArg(program, arg string) error {
	switch program {
	case "sort":
		if hasAnyPrefix(arg, "--output", "--compress-program") || (strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") && strings.Contains(arg, "o")) {
			return fmt.Errorf("sort %s writes a file or runs a program", arg)
		}
	case "tree":
		if arg == "-o" || strings.HasPrefix(arg, "-o") && !strings.HasPrefix(arg, "--") {
			return fmt.Errorf("tree -o writes a file")
		}
	case "less", "more":
		if hasAnyPrefix(arg, "-o", "-O", "--log-file", "--LOG-FILE") {
			return fmt.Errorf("%s %s writes a log file", program, arg)
		}
	case "file":
		if arg == "-C" || arg == "--compile" {
			return fmt.Errorf("file -C writes a file")
		}
	case "jq":
		if strings.Contains(arg, "$ENV") || strings.Contains(arg, "env") {
			return fmt.Errorf("jq env reads the environment")
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
	if err := gitReadOnlyPrefix(sub, args); err != nil {
		return err
	}
	listing, err := gitReadOnlyListing(sub, args)
	if err != nil {
		return err
	}
	return gitReadOnlyChanges(sub, args, listing)
}

func gitReadOnlyPrefix(sub string, args []string) error {
	for _, arg := range args {
		if arg == sub {
			break
		}
		if arg == "-c" || hasAnyPrefix(arg, "--config-env", "--exec-path") {
			return fmt.Errorf("git %s sets configuration that can run programs", arg)
		}
	}
	return nil
}

func gitReadOnlyListing(sub string, args []string) (bool, error) {
	listing := false
	for _, arg := range args {
		if hasAnyPrefix(arg, "--output", "--open-files-in-pager", "--ext-diff") || (sub == "grep" && strings.HasPrefix(arg, "-O")) {
			return false, fmt.Errorf("git %s %s writes a file or runs a program", sub, arg)
		}
		if gitListing[strings.SplitN(arg, "=", 2)[0]] {
			listing = true
		}
	}
	return listing, nil
}

func gitReadOnlyChanges(sub string, args []string, listing bool) error {
	afterSubcommand := false
	for _, arg := range args {
		if !afterSubcommand {
			afterSubcommand = arg == sub
			continue
		}
		if gitChanges[sub][arg] {
			return fmt.Errorf("git %s %s changes the repository", sub, arg)
		}
		if (sub == "branch" || sub == "tag") && !listing && !strings.HasPrefix(arg, "-") {
			return fmt.Errorf("git %s %s creates a %s", sub, arg, sub)
		}
	}
	return nil
}

// sedReadOnly refuses in-place edits, script files and scripts that
// write files or run commands.
func sedReadOnly(args []string) error {
	var parsed sedArguments
	for index := 0; index < len(args); index++ {
		consumedNext, err := parsed.consume(args[index:])
		if err != nil {
			return err
		}
		if consumedNext {
			index++
		}
	}
	if !parsed.explicit && len(parsed.files) > 0 {
		parsed.scripts = append(parsed.scripts, parsed.files[0])
	}
	for _, script := range parsed.scripts {
		if sedScriptUnsafe(script) {
			return fmt.Errorf("sed script may write files or run commands")
		}
	}
	return nil
}

type sedArguments struct {
	scripts  []string
	files    []string
	explicit bool
}

// consume reports whether it used the next argument as a script.
func (p *sedArguments) consume(args []string) (bool, error) {
	arg := args[0]
	switch {
	case arg == "--in-place" || strings.HasPrefix(arg, "--in-place="):
		return false, fmt.Errorf("sed -i edits files")
	case arg == "--file" || strings.HasPrefix(arg, "--file="):
		return false, fmt.Errorf("sed -f runs a script from a file")
	case arg == "--expression" || strings.HasPrefix(arg, "--expression="):
		p.explicit = true
		if value, ok := strings.CutPrefix(arg, "--expression="); ok {
			p.scripts = append(p.scripts, value)
			return false, nil
		}
		if len(args) > 1 {
			p.scripts = append(p.scripts, args[1])
			return true, nil
		}
	case strings.HasPrefix(arg, "--"):
	case len(arg) > 1 && arg[0] == '-':
		return p.consumeShort(args)
	default:
		p.files = append(p.files, arg)
	}
	return false, nil
}

func (p *sedArguments) consumeShort(args []string) (bool, error) {
	arg := args[0]
	for index := 1; index < len(arg); index++ {
		switch arg[index] {
		case 'i', 'I':
			return false, fmt.Errorf("sed -i edits files")
		case 'f':
			return false, fmt.Errorf("sed -f runs a script from a file")
		case 'e':
			p.explicit = true
			if index+1 < len(arg) {
				p.scripts = append(p.scripts, arg[index+1:])
				return false, nil
			}
			if len(args) > 1 {
				p.scripts = append(p.scripts, args[1])
				return true, nil
			}
			return false, nil
		}
	}
	return false, nil
}

// sedScriptUnsafe reports a script with the w, W, r, R or e commands or
// the s command's w or e flags. A script it cannot follow counts as
// unsafe.
func sedScriptUnsafe(script string) bool {
	parser := sedScriptParser{script: script}
	for {
		parser.skip(" \t\n;{}!")
		if parser.done() {
			return false
		}
		if !parser.readAddress() {
			return true
		}
		parser.skip(" ")
		if !parser.done() && parser.current() == ',' {
			parser.pos++
			parser.skip(" ")
			if !parser.readAddress() {
				return true
			}
		}
		parser.skip(" !")
		if parser.done() {
			return false
		}
		command := parser.current()
		parser.pos++
		if parser.unsafeCommand(command) {
			return true
		}
	}
}

type sedScriptParser struct {
	script string
	pos    int
}

func (p *sedScriptParser) done() bool    { return p.pos >= len(p.script) }
func (p *sedScriptParser) current() byte { return p.script[p.pos] }

func (p *sedScriptParser) skip(chars string) {
	for !p.done() && strings.IndexByte(chars, p.current()) >= 0 {
		p.pos++
	}
}

func (p *sedScriptParser) readAddress() bool {
	if p.done() {
		return true
	}
	c := p.current()
	switch {
	case c >= '0' && c <= '9':
		for !p.done() && (p.current() >= '0' && p.current() <= '9' || p.current() == '~') {
			p.pos++
		}
	case c == '$':
		p.pos++
	case c == '/' || c == '\\':
		delim := byte('/')
		if c == '\\' {
			p.pos++
			if p.done() {
				return false
			}
			delim = p.current()
		}
		p.pos++
		if !p.readDelimited(delim) {
			return false
		}
		p.skip("IM")
	}
	return true
}

// readDelimited consumes through an unescaped delimiter, failing closed on EOF.
func (p *sedScriptParser) readDelimited(delim byte) bool {
	for !p.done() && p.current() != delim {
		if p.current() == '\\' {
			p.pos++
		}
		p.pos++
	}
	if p.done() {
		return false
	}
	p.pos++
	return true
}

func (p *sedScriptParser) unsafeCommand(command byte) bool {
	switch command {
	case 'e', 'w', 'W', 'r', 'R':
		return true
	case 's', 'y':
		return p.unsafeSubstitution(command)
	case 'a', 'i', 'c', '#':
		p.skipUntil("\n")
	case ':', 'b', 't', 'T', 'q', 'Q', 'l', 'L':
		p.skipUntil("\n;}")
	case '{', '}', 'p', 'P', 'd', 'D', 'n', 'N', 'g', 'G', 'h', 'H', 'x', '=', 'z', 'F':
	default:
		return true
	}
	return false
}

func (p *sedScriptParser) unsafeSubstitution(command byte) bool {
	if p.done() || p.current() == '\\' || p.current() == '\n' {
		return true
	}
	delim := p.current()
	p.pos++
	for part := 0; part < 2; part++ {
		if !p.readDelimited(delim) {
			return true
		}
	}
	for command == 's' && !p.done() && strings.IndexByte("gpiImMew0123456789", p.current()) >= 0 {
		if p.current() == 'e' || p.current() == 'w' {
			return true
		}
		p.pos++
	}
	return false
}

func (p *sedScriptParser) skipUntil(delimiters string) {
	for !p.done() && strings.IndexByte(delimiters, p.current()) < 0 {
		p.pos++
	}
}
