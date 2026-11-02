// Secret masking for memory writes.
//
// Memory is replayed into every future prompt and lives in a plain file
// the user may sync or share, so a credential pasted into an entry is a
// durable leak. Writes therefore replace secret-shaped values with a
// fixed marker before the entry is stored: the fact survives ("the API
// key lives in 1Password"), the value does not.
//
// Masking is the complement of the hardcoded-secret threat rule in
// threats.go: that rule refuses the write, this one keeps it. Runs first,
// so the strict scan still judges whatever text is left.

package memory

import (
	"regexp"
	"strings"
)

// Masked is what a replaced secret leaves behind. It reads as a value, so
// surrounding prose still parses.
const Masked = "[REDACTED]"

type secretPattern struct {
	re *regexp.Regexp
	// group is the submatch index to replace; 0 replaces the whole match.
	group int
	// structural marks a shape that is a secret by construction (a PEM
	// block, a URI password), so the placeholder heuristic is skipped.
	structural bool
}

// Masked patterns, most specific first. Every one captures only the value
// so surrounding syntax — quotes, a variable name, a URI scheme — stays
// readable.
var secretPatterns = []secretPattern{
	// PEM private key bodies: the armour stays, the key material goes.
	{re: regexp.MustCompile(`(?s)(-----BEGIN [A-Z ]*PRIVATE KEY-----)(.*?)(-----END [A-Z ]*PRIVATE KEY-----)`), group: 2, structural: true},
	// Whole well-known token shapes.
	{re: regexp.MustCompile(`\b(?:sk-[A-Za-z0-9_-]{20,}|sk-proj-[A-Za-z0-9_-]{20,}|ghp_[A-Za-z0-9]{30,}|gho_[A-Za-z0-9]{30,}|ghu_[A-Za-z0-9]{30,}|ghs_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{20,}|AKIA[0-9A-Z]{16}|ASIA[0-9A-Z]{16}|xox[baprs]-[A-Za-z0-9-]{10,}|AIza[0-9A-Za-z_-]{35}|ya29\.[0-9A-Za-z_-]{20,}|glpat-[A-Za-z0-9_-]{20,}|npm_[A-Za-z0-9]{36}\b|pypi-[A-Za-z0-9_-]{50,})\b`), group: 0},
	// "Bearer <token>" keeps the scheme.
	{re: regexp.MustCompile(`(?i)(\bbearer\s+)([A-Za-z0-9._~+/=-]{20,})`), group: 2},
	// Scheme://user:password@host — keep the scheme and the user name.
	{re: regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://[^:/\s@]+:)([^@/\s]{3,})(@)`), group: 2, structural: true},
	// KEY=value / "api_key": "value" / password: value — keep the name.
	{re: regexp.MustCompile(`(?i)\b([A-Za-z0-9_.-]*(?:api[_-]?key|access[_-]?key|secret|token|password|passwd|passphrase|credential|private[_-]?key)[A-Za-z0-9_.-]*)\s*[:=]\s*["']?([^\s"',]{8,})`), group: 2},
	// AWS secret access key with its name.
	{re: regexp.MustCompile(`(?i)\b(aws[_-]?secret[_-]?access[_-]?key)\s*[:=]\s*["']?([A-Za-z0-9/+=]{30,})`), group: 2},
}

// maskSecrets replaces secret-shaped values with Masked and reports
// whether anything changed.
func maskSecrets(content string) (string, bool) {
	if content == "" {
		return content, false
	}
	out := content
	for _, p := range secretPatterns {
		out = p.apply(out)
	}
	return out, out != content
}

func (p secretPattern) apply(text string) string {
	locs := p.re.FindAllStringSubmatchIndex(text, -1)
	if len(locs) == 0 {
		return text
	}
	var b strings.Builder
	last := 0
	replaced := false
	for _, loc := range locs {
		start, end := loc[0], loc[1]
		if p.group > 0 {
			g := p.group * 2
			if g+1 >= len(loc) || loc[g] < 0 {
				continue
			}
			start, end = loc[g], loc[g+1]
		}
		if start < last || end <= start {
			continue // overlaps a value already replaced
		}
		// A bare placeholder, an env-var reference or our own marker is
		// not a secret; leave it exactly as typed. Structures that are
		// secret by construction bypass the heuristic.
		if !p.structural && !looksLikeSecret(text[start:end]) {
			continue
		}
		b.WriteString(text[last:start])
		b.WriteString(Masked)
		last = end
		replaced = true
	}
	if !replaced {
		return text
	}
	b.WriteString(text[last:])
	return b.String()
}

// looksLikeSecret drops values that are clearly names or placeholders:
// an env-var reference, a SHOUTY_SNAKE name, or a template marker. A real
// credential that happens to be SHOUTY_SNAKE is still caught by the
// token-shape rules above.
func looksLikeSecret(value string) bool {
	v := strings.TrimSpace(value)
	// Our own marker is never re-masked, so masking is idempotent.
	if v == Masked || strings.Contains(v, Masked) {
		return false
	}
	if len(v) < 8 {
		return false
	}
	if strings.HasPrefix(v, "$") || strings.HasPrefix(v, "<") || strings.HasPrefix(v, "{{") {
		return false
	}
	switch strings.ToLower(v) {
	case "redacted", "changeme", "placeholder", "example", "your_key_here", "none", "null", "true", "false":
		return false
	}
	if shoutySnake.MatchString(v) {
		return false
	}
	// A path or a filename is not a credential.
	if strings.ContainsAny(v, "/\\ ") && !strings.Contains(v, "=") {
		return false
	}
	return true
}
