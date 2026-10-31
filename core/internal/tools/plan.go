// plan.go is the read-only guard for plan mode. The
// model may inspect the workspace (list, read, search, git history) but
// any command that could change state is refused before it runs.

package tools

import (
	"encoding/json"
	"fmt"
	"strings"
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
	segments, err := splitShellSegments(cmd)
	if err != nil {
		return err
	}
	for _, seg := range segments {
		words := shellWords(seg)
		for len(words) > 0 && isEnvAssign(words[0]) {
			words = words[1:]
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
// and rejects output redirection (other than to /dev/null).
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
			rest := strings.TrimLeft(string(runes[i+1:]), ">& ")
			if strings.HasPrefix(rest, "/dev/null") || (i+1 < len(runes) && runes[i+1] == '&') {
				cur.WriteRune(r)
				continue
			}
			return nil, fmt.Errorf("output redirection is not allowed")
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
		if sub == "branch" || sub == "tag" || sub == "remote" {
			for _, a := range args {
				switch a {
				case "-d", "-D", "-m", "-M", "--delete", "--move", "add", "remove", "rm", "set-url", "rename", "--unset", "--add", "--replace-all", "-f", "--force":
					return fmt.Errorf("git %s %s changes the repository", sub, a)
				}
			}
		}
	case "go":
		if len(args) == 0 || !readOnlyGo[args[0]] {
			return fmt.Errorf("go %s is not read-only", strings.Join(args, " "))
		}
	case "sed":
		for _, a := range args {
			if strings.HasPrefix(a, "-i") || a == "--in-place" || strings.HasPrefix(a, "--in-place=") {
				return fmt.Errorf("sed -i edits files")
			}
		}
	case "find":
		for _, a := range args {
			switch a {
			case "-delete", "-exec", "-execdir", "-ok", "-okdir", "-fprint", "-fprintf", "-fls":
				return fmt.Errorf("find %s is not read-only", a)
			}
		}
	case "awk":
		for _, a := range args {
			if strings.Contains(a, "system(") || strings.Contains(a, "print >") || strings.Contains(a, "| getline") {
				return fmt.Errorf("awk program may run commands or write files")
			}
			if strings.Contains(a, "ENVIRON") {
				return fmt.Errorf("awk ENVIRON reads the environment")
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
		if err := ReadOnlyCommand(args.Cmd); err != nil {
			return "", ArgsError(fmt.Sprintf("plan mode: %v. Plan mode is read-only; describe the change in your plan, "+
				"and the user can switch to build mode to carry it out.", err))
		}
	}
	return p.inner.Call(raw, ctx)
}
