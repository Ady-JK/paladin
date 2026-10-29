package tools

import (
	"encoding/json"
	"strings"
)

// resolveName maps a model-emitted tool name to a registered one,
// tolerating whitespace, case, "-" vs "_" and namespace prefixes some
// models add ("functions.read", "tools.read").
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
	return "", false
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
