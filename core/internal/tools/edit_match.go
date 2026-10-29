package tools

import (
	"strings"
)

// Edit match strategies, tried in order; the first that matches wins.
const (
	MatchExact       = "exact"
	MatchLineEndings = "line_endings"
	MatchWhitespace  = "whitespace"
	MatchIndentation = "indentation"
)

// editSpan is one replacement in the original content (byte offsets).
type editSpan struct {
	start, end int
	repl       string
}

// fuzzyMatch finds old_string in content when the exact search failed,
// tolerating the drift models typically introduce: LF vs CRLF, trailing
// whitespace, typographic quotes/dashes/spaces, a BOM, and a different
// indentation level (new_string is re-indented to match the file).
func fuzzyMatch(content, oldText, newText string) (string, []editSpan) {
	if spans := lineEndingMatch(content, oldText, newText); len(spans) > 0 {
		return MatchLineEndings, spans
	}
	if spans := lineMatch(content, oldText, newText, normTrailing, false); len(spans) > 0 {
		return MatchWhitespace, spans
	}
	if spans := lineMatch(content, oldText, newText, normIndent, true); len(spans) > 0 {
		return MatchIndentation, spans
	}
	return "", nil
}

func lineEndingMatch(content, oldText, newText string) []editSpan {
	var o, n string
	switch {
	case strings.Contains(content, "\r\n") && !strings.Contains(oldText, "\r"):
		o, n = toCRLF(oldText), toCRLF(newText)
	case !strings.Contains(content, "\r\n") && strings.Contains(oldText, "\r\n"):
		o, n = toLF(oldText), toLF(newText)
	default:
		return nil
	}
	var spans []editSpan
	for from := 0; ; {
		i := strings.Index(content[from:], o)
		if i < 0 {
			return spans
		}
		start := from + i
		spans = append(spans, editSpan{start: start, end: start + len(o), repl: n})
		from = start + len(o)
	}
}

// lineMatch compares whole lines under norm. Spans cover the matched
// lines' text without the final line terminator, which is kept.
func lineMatch(content, oldText, newText string, norm func(string) string, reindent bool) []editSpan {
	oldBody := strings.TrimSuffix(toLF(oldText), "\n")
	oldLines := strings.Split(oldBody, "\n")
	want := make([]string, len(oldLines))
	blank := true
	for i, l := range oldLines {
		want[i] = norm(l)
		if want[i] != "" {
			blank = false
		}
	}
	if blank {
		return nil
	}
	repl := toLF(newText)
	if strings.HasSuffix(toLF(oldText), "\n") {
		repl = strings.TrimSuffix(repl, "\n")
	}
	crlf := strings.Contains(content, "\r\n")
	lines := strings.Split(content, "\n")
	starts := make([]int, len(lines))
	for i, off := 0, 0; i < len(lines); i++ {
		starts[i] = off
		off += len(lines[i]) + 1
	}
	var spans []editSpan
	for i := 0; i+len(want) <= len(lines); i++ {
		ok := true
		for k := range want {
			if norm(lines[i+k]) != want[k] {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		last := i + len(want) - 1
		start := starts[i]
		if i == 0 {
			start += len(bomPrefix(lines[0]))
		}
		end := starts[last] + len(strings.TrimSuffix(lines[last], "\r"))
		r := repl
		if reindent {
			r = reindentText(r, leadingWS(stripBOM(lines[i])), leadingWS(oldLines[0]))
		}
		if crlf {
			r = toCRLF(r)
		}
		spans = append(spans, editSpan{start: start, end: end, repl: r})
		i = last
	}
	return spans
}

func reindentText(text, fileIndent, oldIndent string) string {
	if fileIndent == oldIndent {
		return text
	}
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		if strings.TrimSpace(l) == "" || !strings.HasPrefix(l, oldIndent) {
			continue
		}
		lines[i] = fileIndent + l[len(oldIndent):]
	}
	return strings.Join(lines, "\n")
}

func leadingWS(s string) string {
	return s[:len(s)-len(strings.TrimLeft(s, " \t"))]
}

const bom = "\ufeff"

func bomPrefix(s string) string {
	if strings.HasPrefix(s, bom) {
		return bom
	}
	return ""
}

func stripBOM(s string) string { return strings.TrimPrefix(s, bom) }

var typographic = strings.NewReplacer(
	"\u2018", "'", "\u2019", "'", "\u201a", "'", "\u201b", "'", "\u2032", "'",
	"\u201c", `"`, "\u201d", `"`, "\u201e", `"`, "\u201f", `"`, "\u2033", `"`,
	"\u2010", "-", "\u2011", "-", "\u2012", "-", "\u2013", "-", "\u2014", "-", "\u2015", "-", "\u2212", "-",
	"\u00a0", " ", "\u2002", " ", "\u2003", " ", "\u2004", " ", "\u2005", " ", "\u2006", " ",
	"\u2007", " ", "\u2008", " ", "\u2009", " ", "\u200a", " ", "\u202f", " ", "\u205f", " ", "\u3000", " ",
	"\u2026", "...",
)

func fold(s string) string { return typographic.Replace(stripBOM(s)) }

func normTrailing(s string) string { return strings.TrimRight(fold(s), " \t\r") }

func normIndent(s string) string { return strings.TrimSpace(fold(s)) }

func toLF(s string) string { return strings.ReplaceAll(s, "\r\n", "\n") }

func toCRLF(s string) string { return strings.ReplaceAll(toLF(s), "\n", "\r\n") }

// applySpans rewrites content with non-overlapping, ordered spans.
func applySpans(content string, spans []editSpan) string {
	var b strings.Builder
	prev := 0
	for _, s := range spans {
		b.WriteString(content[prev:s.start])
		b.WriteString(s.repl)
		prev = s.end
	}
	b.WriteString(content[prev:])
	return b.String()
}

// spanOccurrences reports spans the way FindOccurrences reports exact
// hits, so ambiguity errors look the same for every strategy.
func spanOccurrences(content string, spans []editSpan) []Occurrence {
	lines := strings.Split(content, "\n")
	starts := make([]int, len(lines))
	for i, off := 0, 0; i < len(lines); i++ {
		starts[i] = off
		off += len(lines[i]) + 1
	}
	out := make([]Occurrence, 0, len(spans))
	for i, s := range spans {
		end := s.end - 1
		if end < s.start {
			end = s.start
		}
		sl, el := lineOf(starts, s.start), lineOf(starts, end)
		out = append(out, Occurrence{
			Occurrence: i + 1, StartLine: sl, EndLine: el,
			Before: nearestNonBlank(lines, sl-1, -1),
			After:  nearestNonBlank(lines, el+1, 1),
		})
	}
	return out
}
