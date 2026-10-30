// Package redact masks secrets in text that leaves a tool or the
// sidecar: shell output, error messages and logs. It is a best-effort
// pattern match (ported from Hermes' redact.py): known key prefixes,
// bearer tokens, URL credentials, secret-named KEY=value assignments,
// PEM private keys and registered values such as stored provider keys.
// File contents returned by `read` are not masked, so edits still match.
package redact

import (
	"regexp"
	"sort"
	"strings"
	"sync"
)

const minRegistered = 8

var (
	prefixRE = regexp.MustCompile(`(^|[^A-Za-z0-9_])(` + strings.Join([]string{
		`sk-[A-Za-z0-9_-](?:\.?[A-Za-z0-9_-]){9,}`,
		`gh[pousr]_[A-Za-z0-9]{10,}`,
		`github_pat_[A-Za-z0-9_]{10,}`,
		`xox[baprs]-[A-Za-z0-9-]{10,}`,
		`AIza[A-Za-z0-9_-]{30,}`,
		`AKIA[A-Z0-9]{16}`,
		`[sr]k_(?:live|test)_[A-Za-z0-9]{10,}`,
		`SG\.[A-Za-z0-9_-]{10,}`,
		`hf_[A-Za-z0-9]{10,}`,
		`npm_[A-Za-z0-9]{10,}`,
		`pypi-[A-Za-z0-9_-]{10,}`,
		`glpat-[A-Za-z0-9_-]{10,}`,
		`gsk_[A-Za-z0-9]{10,}`,
		`xai-[A-Za-z0-9]{30,}`,
	}, "|") + `)`)
	bearerRE  = regexp.MustCompile(`(?i)(\bbearer\s+)([A-Za-z0-9._~+/=-]{8,})`)
	urlCredRE = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://[^\s:/@]+:)([^\s/@]+)@`)
	envRE     = regexp.MustCompile(`(^|[^A-Za-z0-9_])([A-Z0-9_]*(?:API_?KEY|SECRET|TOKEN|PASSWORD|PASSWD|CREDENTIALS?|PRIVATE_KEY|ACCESS_KEY|AUTH)[A-Z0-9_]*=)(['"]?)([^\s'"]{8,})`)
	pemRE     = regexp.MustCompile(`(?s)-----BEGIN ([A-Z ]*)PRIVATE KEY-----.*?-----END ([A-Z ]*)PRIVATE KEY-----`)

	mu         sync.RWMutex
	registered = map[string]bool{}
	ordered    []string
)

// mask keeps a recognisable head and tail of long values.
func mask(v string) string {
	if len(v) < 16 {
		return "[redacted]"
	}
	return v[:6] + "…" + v[len(v)-4:]
}

// String masks every secret it recognises in s.
func String(s string) string {
	if s == "" {
		return s
	}
	mu.RLock()
	vals := ordered
	mu.RUnlock()
	for _, v := range vals {
		if strings.Contains(s, v) {
			s = strings.ReplaceAll(s, v, "[redacted]")
		}
	}
	s = pemRE.ReplaceAllString(s, "-----BEGIN ${1}PRIVATE KEY----- [redacted] -----END ${2}PRIVATE KEY-----")
	s = replaceGroup(prefixRE, s, 2)
	s = replaceGroup(bearerRE, s, 2)
	s = replaceGroup(urlCredRE, s, 2)
	s = replaceGroup(envRE, s, 4)
	return s
}

// replaceGroup masks submatch g of every match, keeping the rest.
func replaceGroup(re *regexp.Regexp, s string, g int) string {
	idx := re.FindAllStringSubmatchIndex(s, -1)
	if idx == nil {
		return s
	}
	var b strings.Builder
	last := 0
	for _, m := range idx {
		start, end := m[2*g], m[2*g+1]
		if start < 0 || strings.HasPrefix(s[start:end], "$") {
			continue
		}
		b.WriteString(s[last:start])
		b.WriteString(mask(s[start:end]))
		last = end
	}
	b.WriteString(s[last:])
	return b.String()
}

// Register adds an exact value to mask (e.g. a stored API key). Values
// shorter than 8 bytes are ignored: they would mask ordinary words.
func Register(v string) {
	v = strings.TrimSpace(v)
	if len(v) < minRegistered {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	if registered[v] {
		return
	}
	registered[v] = true
	rebuild()
}

// Unregister forgets a value passed to Register.
func Unregister(v string) {
	mu.Lock()
	defer mu.Unlock()
	delete(registered, strings.TrimSpace(v))
	rebuild()
}

// Reset forgets every registered value (tests).
func Reset() {
	mu.Lock()
	defer mu.Unlock()
	registered = map[string]bool{}
	ordered = nil
}

// rebuild orders values longest first so a value containing another
// is masked whole.
func rebuild() {
	out := make([]string, 0, len(registered))
	for v := range registered {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	ordered = out
}
