package permission

import (
	"regexp"
	"strings"
)

// scan splits a shell command into pipelines of segments of words, with
// quotes removed and redirect targets set apart. Command substitutions
// ($(...), backticks, <(...)) are returned as text to inspect on their
// own. It is a reader for the guard, not a shell: odd input yields odd
// words, never an error.
func scan(cmd string) (pipelines [][]segment, subs []string) {
	var (
		pipe     []segment
		seg      segment
		word     strings.Builder
		have     bool
		redirTo  bool // the next word is a redirect target
		redirFd  bool // the next word is a file descriptor (2>&1), not a file
		inSingle bool
		inDouble bool
	)
	endWord := func() {
		if !have {
			return
		}
		w := word.String()
		word.Reset()
		have = false
		switch {
		case redirFd:
			redirFd = false
		case redirTo:
			seg.redirs = append(seg.redirs, w)
			redirTo = false
		default:
			seg.words = append(seg.words, w)
		}
	}
	endSegment := func() {
		endWord()
		redirTo, redirFd = false, false
		if len(seg.words) > 0 || len(seg.redirs) > 0 || len(seg.subs) > 0 || seg.procSub {
			pipe = append(pipe, seg)
		}
		seg = segment{}
	}
	endPipeline := func() {
		endSegment()
		if len(pipe) > 0 {
			pipelines = append(pipelines, pipe)
		}
		pipe = nil
	}
	grab := func(s string, i int, open, closeCh byte) (inner string, next int) {
		depth := 0
		for j := i; j < len(s); j++ {
			switch {
			case s[j] == open && open != closeCh:
				depth++
			case s[j] == closeCh:
				if depth == 0 {
					return s[i:j], j + 1
				}
				depth--
			}
		}
		return s[i:], len(s)
	}

	for i := 0; i < len(cmd); {
		c := cmd[i]
		switch {
		case c == '\\' && !inSingle && i+1 < len(cmd):
			word.WriteByte(cmd[i+1])
			have = true
			i += 2
		case inSingle:
			if c == '\'' {
				inSingle = false
			} else {
				word.WriteByte(c)
			}
			i++
		case c == '$' && i+1 < len(cmd) && cmd[i+1] == '(':
			inner, next := grab(cmd, i+2, '(', ')')
			subs, seg.subs = append(subs, inner), append(seg.subs, inner)
			have = true
			i = next
		case c == '`':
			inner, next := grab(cmd, i+1, '`', '`')
			subs, seg.subs = append(subs, inner), append(seg.subs, inner)
			have = true
			i = next
		case c == '"':
			inDouble = !inDouble
			have = true
			i++
		case inDouble:
			word.WriteByte(c)
			i++
		case c == '\'':
			inSingle, have = true, true
			i++
		case (c == '<' || c == '>') && i+1 < len(cmd) && cmd[i+1] == '(':
			inner, next := grab(cmd, i+2, '(', ')')
			subs, seg.subs = append(subs, inner), append(seg.subs, inner)
			seg.procSub = true
			i = next
		case c == ' ' || c == '\t':
			endWord()
			i++
		case c == '\n' || c == ';':
			endPipeline()
			i++
		case c == '&' && i+1 < len(cmd) && cmd[i+1] == '&':
			endPipeline()
			i += 2
		case c == '|' && i+1 < len(cmd) && cmd[i+1] == '|':
			endPipeline()
			i += 2
		case c == '|':
			endSegment()
			i++
		case c == '&':
			endPipeline()
			i++
		case c == '>' || c == '<':
			// A digit just before it is a descriptor (2>), not an argument.
			if have && isDigits(word.String()) {
				word.Reset()
				have = false
			} else {
				endWord()
			}
			i++
			if i < len(cmd) && cmd[i] == c {
				i++
			}
			if c == '>' && i < len(cmd) && cmd[i] == '&' {
				redirFd = true
				i++
			} else if c == '>' {
				redirTo = true
			}
		default:
			word.WriteByte(c)
			have = true
			i++
		}
	}
	endPipeline()
	return pipelines, subs
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

var assignment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// wrapper options that take a value, per wrapper.
var wrapperValueOpts = map[string]map[string]bool{
	"sudo":   {"-u": true, "-g": true, "-h": true, "-p": true, "-C": true, "-D": true, "-R": true, "-T": true, "-U": true},
	"doas":   {"-u": true, "-C": true},
	"xargs":  {"-I": true, "-n": true, "-P": true, "-L": true, "-d": true, "-E": true, "-s": true},
	"env":    {"-u": true, "-C": true, "-S": true},
	"nice":   {"-n": true},
	"ionice": {"-c": true, "-n": true, "-p": true},
	"stdbuf": {"-i": true, "-o": true, "-e": true},
}

var wrappers = set("sudo", "doas", "env", "nohup", "time", "command", "exec", "builtin", "xargs", "nice", "ionice",
	"stdbuf", "timeout", "setsid", "caffeinate", "unbuffer")

// unwrap drops leading assignments and wrapper commands (sudo, env,
// timeout 5, nice -n 5, xargs ...) and returns the real command's words.
func unwrap(words []string) []string {
	for len(words) > 0 {
		w := words[0]
		if assignment.MatchString(w) {
			words = words[1:]
			continue
		}
		name := commandName(w)
		if !wrappers[name] {
			return words
		}
		words = words[1:]
		valued := wrapperValueOpts[name]
		for len(words) > 0 {
			o := words[0]
			if assignment.MatchString(o) && name == "env" {
				words = words[1:]
				continue
			}
			if !strings.HasPrefix(o, "-") {
				break
			}
			words = words[1:]
			if valued[o] && len(words) > 0 {
				words = words[1:]
			}
		}
		if name == "timeout" && len(words) > 0 {
			words = words[1:] // the duration
		}
	}
	return words
}
