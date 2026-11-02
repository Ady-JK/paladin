// Threat patterns for memory writes.
//
// Memory entries are replayed into every future system prompt, so a
// poisoned entry is a persistent jailbreak: every write is scanned before
// it is accepted, and Render re-scans hand-edited files at display time.
// The set follows Hermes' threat_patterns.py in strict scope — classic
// injection everywhere, role hijack and promptware vocabulary, then
// exfiltration, persistence and hardcoded-secret shapes for durable
// stores.
//
// Two deliberate adaptations: Hermes' NFKC homograph folding needs
// golang.org/x/text, which is not a dependency here, so matching is
// direct; and Hermes' negative-lookahead secret rule becomes a match plus
// a code-side SHOUTY_SNAKE check because Go's regexp has no lookahead.

package memory

import (
	"regexp"
	"strings"
)

// maxScanChars bounds one scan so worst-case runtime stays flat even on a
// hand-edited megabyte file.
const maxScanChars = 65536

type threatPattern struct {
	re     *regexp.Regexp
	reason string
	// skipValue, when set, is tested against the match's first capture
	// group: a hit there means the match is benign — a credential NAME,
	// not a credential.
	skipValue *regexp.Regexp
}

// shoutySnake matches ENV_PASSWORD = "MYPLUGIN_APP_PASSWORD": it says
// where a credential lives, it does not embed one. Case-sensitive on
// purpose, because a lowercase snake value is the passphrase shape.
var shoutySnake = regexp.MustCompile(`^[A-Z][A-Z0-9]*(_[A-Z0-9]+)+$`)

var threatPatterns = []threatPattern{
	// Classic prompt injection.
	{re: regexp.MustCompile(`(?i)ignore\s+(?:\w+\s+){0,8}(previous|all|above|prior)\s+(?:\w+\s+){0,8}instructions?`), reason: "instruction override"},
	{re: regexp.MustCompile(`(?i)\b(ignore|disregard|forget|override)\b[^.\n]{0,40}\b(previous|prior|above|earlier|all|system)\b[^.\n]{0,20}\b(instructions?|prompts?|rules?|messages?)`), reason: "instruction override"},
	{re: regexp.MustCompile(`(?i)system\s+prompt\s+override`), reason: "system prompt override"},
	{re: regexp.MustCompile(`(?i)disregard\s+(?:\w+\s+){0,8}(your|all|any)\s+(?:\w+\s+){0,8}(instructions|rules|guidelines)`), reason: "instruction override"},
	{re: regexp.MustCompile(`(?i)act\s+as\s+(if|though)\s+(?:\w+\s+){0,8}you\s+(?:\w+\s+){0,8}(have\s+no|don't\s+have)\s+(?:\w+\s+){0,8}(restrictions|limits|rules)`), reason: "restriction bypass"},
	{re: regexp.MustCompile(`(?i)<!--[^>]*(?:ignore|override|system|secret|hidden)[^>]*-->`), reason: "hidden HTML instruction"},
	{re: regexp.MustCompile(`(?i)<\s*div\s+style\s*=\s*["'][^>]*display\s*:\s*none`), reason: "hidden content"},
	{re: regexp.MustCompile(`(?i)translate\s+[^\n]*\s+into\s+\w+(?:[\s-]+\w+){0,2}\s+and\s+(execute|run|eval)\b`), reason: "translate-and-execute"},
	{re: regexp.MustCompile(`(?i)do\s+not\s+(?:\w+\s+){0,8}tell\s+(?:\w+\s+){0,8}the\s+user`), reason: "hide from user"},

	// Role-play and identity hijack.
	{re: regexp.MustCompile(`(?i)\byou are now\b|\bnew (system )?instructions?:`), reason: "role hijack"},
	{re: regexp.MustCompile(`(?i)you\s+are\s+(?:\w+\s+){0,8}now\s+(?:a|an|the)\s+`), reason: "role hijack"},
	{re: regexp.MustCompile(`(?i)pretend\s+(?:\w+\s+){0,8}(you\s+are|to\s+be)\s+`), reason: "role hijack"},
	{re: regexp.MustCompile(`(?i)\b(reveal|print|show|output|leak)\b[^.\n]{0,30}\bsystem prompt\b`), reason: "system prompt extraction"},
	{re: regexp.MustCompile(`(?i)output\s+(?:\w+\s+){0,8}(system|initial)\s+prompt`), reason: "system prompt extraction"},
	{re: regexp.MustCompile(`(?i)(respond|answer|reply)\s+without\s+(?:\w+\s+){0,8}(restrictions|limitations|filters|safety)`), reason: "filter removal"},
	{re: regexp.MustCompile(`(?i)you\s+have\s+been\s+(?:\w+\s+){0,8}(updated|upgraded|patched)\s+to`), reason: "fake update"},
	{re: regexp.MustCompile(`(?i)\bname\s+yourself\s+\w+`), reason: "identity override"},

	// C2 and promptware vocabulary.
	{re: regexp.MustCompile(`(?i)register\s+(as\s+)?a?\s*node`), reason: "C2 node registration"},
	{re: regexp.MustCompile(`(?i)(heartbeat|beacon|check[\s-]?in)\s+(to|with)\s+`), reason: "C2 beaconing"},
	{re: regexp.MustCompile(`(?i)pull\s+(down\s+)?(?:new\s+)?task(?:ing|s)?\b`), reason: "C2 task pull"},
	{re: regexp.MustCompile(`(?i)connect\s+to\s+the\s+network\b`), reason: "C2 network connect"},
	{re: regexp.MustCompile(`(?i)you\s+must\s+(?:\w+\s+){0,3}(register|connect|report|beacon)\b`), reason: "forced C2 action"},
	{re: regexp.MustCompile(`(?i)only\s+use\s+one[\s-]?liners?\b`), reason: "anti-forensic instruction"},
	{re: regexp.MustCompile(`(?i)never\s+(?:\w+\s+){0,8}(?:create|write)\s+(?:\w+\s+){0,8}(?:script|file)\s+(?:\w+\s+){0,8}disk`), reason: "anti-forensic instruction"},
	{re: regexp.MustCompile(`(?i)unset\s+\w*(?:CLAUDE|CODEX|HERMES|PALADIN|AGENT|OPENAI|ANTHROPIC)\w*`), reason: "agent env unset"},
	{re: regexp.MustCompile(`(?i)\b(?:cobalt\s*strike|sliver|havoc|mythic|metasploit|brainworm)\b`), reason: "known offensive tooling"},
	{re: regexp.MustCompile(`(?i)\bc2\s+(?:server|channel|infrastructure|beacon)\b`), reason: "command and control"},
	{re: regexp.MustCompile(`(?i)\bcommand\s+and\s+control\b`), reason: "command and control"},

	// Exfiltration.
	{re: regexp.MustCompile(`(?i)\b(curl|wget)\b[^\n|]*\|\s*(ba|z)?sh\b`), reason: "remote code execution"},
	{re: regexp.MustCompile(`(?i)curl\s+[^\n]*\$\{?\w*(?:KEY|TOKEN|SECRET|PASSWORD|CREDENTIAL)S?\b`), reason: "secret exfiltration via curl"},
	{re: regexp.MustCompile(`(?i)wget\s+[^\n]*\$\{?\w*(?:KEY|TOKEN|SECRET|PASSWORD|CREDENTIAL)S?\b`), reason: "secret exfiltration via wget"},
	{re: regexp.MustCompile(`(?i)cat\s+[^\n]*(\.env|credentials|\.netrc|\.pgpass|\.npmrc|\.pypirc)`), reason: "credential file read"},
	{re: regexp.MustCompile(`(?i)\b(send|post|upload|exfiltrate)\b[^.\n]{0,40}\b(secrets?|tokens?|api[ _-]?keys?|credentials?|passwords?)\b`), reason: "exfiltration"},
	{re: regexp.MustCompile(`(?i)(send|post|upload|transmit)\s+[^\n]*\s+(to|at)\s+https?://`), reason: "send to URL"},
	{re: regexp.MustCompile(`(?i)(include|output|print|share)\s+(?:\w+\s+){0,8}(conversation|chat\s+history|previous\s+messages|full\s+context|entire\s+context)`), reason: "conversation exfiltration"},

	// Persistence and backdoors.
	{re: regexp.MustCompile(`(?i)authorized_keys`), reason: "SSH backdoor"},
	{re: regexp.MustCompile(`(?i)(?:\b(?:echo|cat|cp|mv|dd|tee|install|printf|rsync|scp|ln|append|add|write|sed|chmod|chown|truncate|rm|touch|curl|wget|git)\b|\bopen\s*\(|>>?)[^\n]*(?:\$HOME/\.ssh|~/\.ssh)`), reason: "SSH directory write"},
	{re: regexp.MustCompile(`(?i)(update|modify|edit|write|change|append|add\s+to)\s+[^\n]*(?:AGENTS\.md|CLAUDE\.md|\.cursorrules|\.clinerules)`), reason: "agent config modification"},
	{re: regexp.MustCompile(`(?i)(update|modify|edit|write|change|append|add\s+to)\s+[^\n]*(?:SYSTEM_PROMPT\.md|settings\.json|providers\.json|memory\.json|tools\.json|hooks\.json|mcp\.json)`), reason: "paladin config modification"},

	// Hardcoded secrets.
	{re: regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`), reason: "private key"},
	{re: regexp.MustCompile(`\b(sk-[A-Za-z0-9_-]{20,}|ghp_[A-Za-z0-9]{30,}|gho_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{20,}|AKIA[0-9A-Z]{16}|xox[baprs]-[A-Za-z0-9-]{10,})\b`), reason: "secret token"},
	{re: regexp.MustCompile(`(?i)(?:api[_-]?key|token|secret|password)\s*[=:]\s*["']?([A-Za-z0-9+/=_-]{20,})`), reason: "hardcoded secret", skipValue: shoutySnake},
}

// isInvisibleRune reports zero-width, bidi-control and tag characters
// used in injection attacks (Trojan Source): text that reads one way to a
// human and another to the model.
func isInvisibleRune(r rune) bool {
	switch {
	case r >= 0x200B && r <= 0x200F,
		r >= 0x202A && r <= 0x202E,
		r >= 0x2060 && r <= 0x2064,
		r >= 0x2066 && r <= 0x2069,
		r == 0xFEFF:
		return true
	}
	return false
}

// Scan reports why content must not enter memory, or "" when clean. The
// reason names the rule that matched so the refusal is actionable, and
// Render uses the same call to withhold hand-edited poisoned entries.
func Scan(content string) string {
	for _, r := range content {
		if isInvisibleRune(r) {
			return "invisible or bidi control characters"
		}
	}
	scan := content
	if len(scan) > maxScanChars {
		scan = strings.ToValidUTF8(scan[:maxScanChars], "")
	}
	for _, p := range threatPatterns {
		m := p.re.FindStringSubmatch(scan)
		if m == nil {
			continue
		}
		if p.skipValue != nil && len(m) > 1 && p.skipValue.MatchString(m[1]) {
			continue
		}
		return p.reason
	}
	return ""
}
