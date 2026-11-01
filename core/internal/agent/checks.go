// checks.go decides whether a shell command was a real check of the
// code (a build, test, linter or type check) and whether it passed.
// The list is deliberately conservative: an unknown command is not a
// check, so it never clears the check-before-finishing reminder.

package agent

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

type checkResult int

const (
	notACheck checkResult = iota
	checkPassed
	checkFailed
	// checkUnknown is a check whose exit status was masked (a pipe,
	// "; echo", "|| true", &) and whose output shows no failure: it
	// neither clears the reminder nor says the check failed.
	checkUnknown
)

// checkOutcome classifies cmd given its exit code and output. The exit
// code is a check's own only when nothing but && follows it (or a pipe
// after set -o pipefail). A check whose status is masked passes only if
// it cannot be told apart: its output may still show a failure (failed),
// and otherwise it is unknown, since tail or grep may have cut the
// failure away.
func checkOutcome(cmd string, exit int, output string) checkResult {
	segs := shellSegments(cmd)
	var checks []int
	pipefail := false
	for i, s := range segs {
		if isCheck(s.text) {
			checks = append(checks, i)
		} else if len(checks) == 0 && pipefailOn.MatchString(s.text) {
			pipefail = true
		}
	}
	if len(checks) == 0 {
		return notACheck
	}
	trusted := func(at int) bool {
		for _, s := range segs[at:] {
			if s.op != "" && s.op != "&&" && (s.op != "|" || !pipefail) {
				return false
			}
		}
		return true
	}
	masked := false
	for _, at := range checks {
		masked = masked || !trusted(at)
	}
	switch {
	case exit != 0 && trusted(checks[len(checks)-1]):
		return checkFailed
	case masked && failureText.MatchString(output):
		return checkFailed
	case masked:
		return checkUnknown
	case exit != 0:
		return checkFailed
	}
	return checkPassed
}

// pipefailOn is a set command turning on pipefail (set -o pipefail,
// set -euo pipefail).
var pipefailOn = regexp.MustCompile(`^set\s+(-[a-zA-Z]*o\s+pipefail|-o\s+pipefail)\b`)

// failureText is what failing builds and tests print.
var failureText = regexp.MustCompile(`(?m)\bFAIL(ED|URE)?\b|\b[1-9]\d* (failed|errors?)\b|^\s*(error|Error|ERROR)\b|\berror(\[\w+\])?:|Traceback \(most recent call last\)|^panic: |npm ERR!|ELIFECYCLE|✗|✖`)

type segment struct {
	text string
	// op is the operator after the segment ("" for the last one).
	op string
}

// shellSegments splits cmd on ;, &&, ||, |, & and newlines outside
// quotes.
func shellSegments(cmd string) []segment {
	var out []segment
	var b strings.Builder
	var quote byte
	flush := func(op string) {
		out = append(out, segment{text: strings.TrimSpace(b.String()), op: op})
		b.Reset()
	}
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
			b.WriteByte(c)
		case c == '\'' || c == '"':
			quote = c
			b.WriteByte(c)
		case c == '\\' && i+1 < len(cmd):
			b.WriteByte(c)
			i++
			b.WriteByte(cmd[i])
		case (c == '&' || c == '|') && i+1 < len(cmd) && cmd[i+1] == c:
			flush(string([]byte{c, c}))
			i++
		case c == '>' || c == '<':
			// Redirections like 2>&1 stay with their segment.
			b.WriteByte(c)
			if i+1 < len(cmd) && cmd[i+1] == '&' {
				i++
				b.WriteByte('&')
			}
		case c == ';' || c == '\n' || c == '|' || c == '&':
			flush(string(c))
		default:
			b.WriteByte(c)
		}
	}
	flush("")
	return out
}

// checkScript names package scripts and make targets that check code.
var checkScript = regexp.MustCompile(`(?i)test|lint|check|build|typecheck|tsc|vet|verify|compile|^ci$`)

// prefixes are words that run the command after them.
var prefixes = map[string]bool{"time": true, "env": true, "command": true, "exec": true, "nice": true, "npx": true, "bunx": true}

// buildGoal is a Maven or Gradle task that checks code.
var buildGoal = regexp.MustCompile(`(?i)^(test|check|verify|build|compile|package|assemble)$`)

// commandWords drops environment assignments and runner prefixes (npx,
// uv run, …) so the words start at the command that does the work.
func commandWords(seg string) []string {
	words := strings.Fields(strings.Trim(seg, "() "))
	for len(words) > 0 {
		w := words[0]
		switch {
		case strings.Contains(w, "=") && !strings.HasPrefix(w, "-"), prefixes[w]:
			words = words[1:]
		case len(words) > 1 && slices.Contains([]string{"uv", "poetry", "pipenv", "hatch", "pdm", "rye"}, w) && words[1] == "run",
			len(words) > 1 && w == "pnpm" && (words[1] == "exec" || words[1] == "dlx"),
			len(words) > 1 && w == "bundle" && words[1] == "exec":
			words = words[2:]
		default:
			return words
		}
	}
	return nil
}

// isCheck reports whether one simple command checks code.
func isCheck(seg string) bool {
	words := commandWords(seg)
	if len(words) == 0 {
		return false
	}
	for _, w := range words {
		if w == "--version" || w == "--help" || w == "-h" || w == "--dry-run" {
			return false
		}
	}
	name, rest := filepath.Base(words[0]), words[1:]
	sub := ""
	if len(rest) > 0 {
		sub = rest[0]
	}
	if strings.HasPrefix(name, "python") {
		if sub != "-m" || len(rest) < 2 {
			return false
		}
		name, rest = rest[1], rest[2:]
		sub = ""
		if len(rest) > 0 {
			sub = rest[0]
		}
	}
	switch name {
	case "tsc", "vue-tsc", "eslint", "vitest", "jest", "mocha", "ava", "pytest", "py.test", "tox", "nox",
		"mypy", "pyright", "basedpyright", "flake8", "pylint", "unittest", "py_compile", "compileall",
		"golangci-lint", "staticcheck", "rustc", "javac", "gcc", "g++", "clang", "clang++", "ctest",
		"shellcheck", "rspec", "phpunit", "stylelint", "xcodebuild":
		return true
	case "go":
		return slices.Contains([]string{"test", "build", "vet"}, sub)
	case "cargo":
		return slices.Contains([]string{"test", "build", "check", "clippy", "nextest"}, sub)
	case "ruff":
		return slices.Contains(rest, "check")
	case "biome":
		return slices.Contains([]string{"check", "lint", "ci"}, sub)
	case "prettier":
		return slices.Contains(rest, "--check")
	case "playwright":
		return sub == "test"
	case "deno":
		return slices.Contains([]string{"test", "check", "lint"}, sub)
	case "dotnet", "swift", "bazel", "bazelisk", "zig":
		return sub == "build" || sub == "test"
	case "node":
		return sub == "--test" || sub == "--check"
	case "cmake":
		return sub == "--build"
	case "npm", "pnpm", "yarn", "bun":
		if sub == "test" || sub == "t" {
			return true
		}
		if (sub == "run" || sub == "run-script") && len(rest) > 1 {
			return checkScript.MatchString(rest[1])
		}
		return name != "npm" && sub != "" && !strings.HasPrefix(sub, "-") &&
			sub != "install" && sub != "add" && checkScript.MatchString(sub)
	case "make", "gmake", "just", "task":
		targets := 0
		for _, w := range rest {
			if strings.HasPrefix(w, "-") || strings.Contains(w, "=") {
				continue
			}
			targets++
			if checkScript.MatchString(w) || w == "all" {
				return true
			}
		}
		return targets == 0 && name != "task"
	case "mvn", "gradle", "gradlew":
		for _, w := range rest {
			if buildGoal.MatchString(w) {
				return true
			}
		}
	}
	return false
}
