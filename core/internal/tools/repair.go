package tools

import (
	"encoding/json"
	"strings"
)

// toolAliases maps a tool name models carry over from other harnesses
// to the tool of ours that does the same job. Keys are normalized
// (lower case, "_" for "-" and spaces), so "Bash", "bash" and
// "functions.bash" all arrive as "bash".
//
// An entry is only safe when the alias takes the same arguments as the
// target: a model asking for `bash` sends {"command": "…"}, which is
// exactly the shell tool's field. A name whose arguments do not match
// (a `patch` body is not an edit's old_string/new_string) must not be
// listed — the model gets "unknown tool" and adapts, rather than
// having its arguments silently misread.
var toolAliases = map[string]string{
	"bash":            "shell",
	"sh":              "shell",
	"execute_bash":    "shell",
	"shell_command":   "shell",
	"run_command":     "shell",
	"execute_command": "shell",
}

// resolveName maps a model-emitted tool name to a registered one,
// tolerating whitespace, case, "-" vs "_" and namespace prefixes some
// models add ("functions.read", "tools.read"), then the cross-harness
// aliases in toolAliases ("bash" is shell).
//
// A registered tool always wins: a registry that really does hold a
// tool called "bash" resolves to it, so nothing is ever shadowed.
func (r *Registry) resolveName(name string) (string, bool) {
	if _, ok := r.tools[name]; ok {
		return name, true
	}
	want := normalizeToolName(name)
	if want == "" {
		return "", false
	}
	for _, n := range r.Names() {
		if normalizeToolName(n) == want {
			return n, true
		}
	}
	return r.aliasTarget(want)
}

// aliasTarget maps a normalized alias to the tool it stands for, when
// this registry holds that tool. A registry without it (a subagent or
// plan-mode subset that dropped shell) leaves the call unresolved, so
// the model is told the tool is unavailable instead of being quietly
// given one it was not granted.
func (r *Registry) aliasTarget(normalized string) (string, bool) {
	target, ok := toolAliases[normalized]
	if !ok || target == "" {
		return "", false
	}
	if _, registered := r.tools[target]; !registered {
		return "", false
	}
	return target, true
}

// CanonicalName is the registered name a model-emitted tool name
// resolves to, or the name unchanged when nothing resolves.
//
// The agent uses it to record an aliased call under the tool that
// really ran: a model asking for "bash" is saved as asking for
// "shell", so the conversation it replays on the next round — and the
// transcript a later turn reads — show the call matching the tool list
// it was given. It is deliberately silent: telling the model about the
// mapping in a tool result invites it to keep using the alias.
func (r *Registry) CanonicalName(name string) string {
	if r == nil {
		return name
	}
	if resolved, ok := r.resolveName(name); ok {
		return resolved
	}
	return name
}

func normalizeToolName(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	for _, prefix := range []string{"functions.", "function.", "tools.", "tool."} {
		n = strings.TrimPrefix(n, prefix)
	}
	n = strings.NewReplacer("-", "_", " ", "_").Replace(n)
	return n
}

// repairArgs fixes light, unambiguous damage in model-emitted JSON: a
// surrounding markdown fence and trailing commas. Anything else (e.g.
// truncation) is left alone so the model sees the error and retries.
func repairArgs(raw string) (string, bool) {
	if json.Valid([]byte(raw)) {
		return raw, true
	}
	s := strings.TrimSpace(raw)
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```")
		if nl := strings.IndexByte(s, '\n'); nl >= 0 && !strings.ContainsAny(s[:nl], "{[") {
			s = s[nl+1:]
		}
		s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "```"))
	}
	s = stripTrailingCommas(s)
	if json.Valid([]byte(s)) {
		return s, true
	}
	return raw, false
}

func stripTrailingCommas(s string) string {
	var b strings.Builder
	inString, escaped := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inString {
			b.WriteByte(c)
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		if c == '"' {
			inString = true
		}
		if c == ',' {
			j := i + 1
			for j < len(s) && strings.IndexByte(" \t\r\n", s[j]) >= 0 {
				j++
			}
			if j < len(s) && (s[j] == '}' || s[j] == ']') {
				continue
			}
		}
		b.WriteByte(c)
	}
	return b.String()
}
