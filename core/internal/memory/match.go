// Tolerant entry addressing for patch and remove.
//
// Models re-type stored text: straight quotes for curly ones, "-" for an
// em dash, unwrapped lines for wrapped ones. A byte-exact substring test
// then reports "no entry matched" for an unambiguous target and the turn
// burns retries. Matching is therefore byte-exact first, typography-folded
// second, and a whole-entry match wins over every substring so a short
// entry stays addressable beside a longer sibling that contains it.

package memory

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// foldTable maps the typography a model re-types differently from the
// stored entry: every quote becomes ', every dash becomes -. Whitespace
// runs (re-wrapped lines, NBSP) collapse to one space while folding.
var foldTable = map[rune]string{
	'“': "'", '”': "'", '‘': "'", '’': "'", '‚': "'", '‛': "'", '„': "'", '‟': "'",
	'"': "'", '`': "'", '´': "'", '′': "'", '″': "'",
	'‐': "-", '‑': "-", '‒': "-", '–': "-", '—': "-", '―': "-", '−': "-", '﹘': "-", '﹣': "-", '－': "-",
	'…': "...",
}

// foldText returns the folded form of text plus, for every byte of the
// folded output, the byte range of the source rune(s) it came from. The
// ranges let a folded match be translated back into an exact span of the
// original entry, which is what patch replaces.
func foldText(text string) (string, []int, []int) {
	var b strings.Builder
	b.Grow(len(text))
	starts := make([]int, 0, len(text))
	ends := make([]int, 0, len(text))
	spaceAt, spaceEnd := -1, -1
	flushSpace := func() {
		if spaceAt < 0 {
			return
		}
		if b.Len() > 0 { // a leading space is dropped
			b.WriteByte(' ')
			starts = append(starts, spaceAt)
			ends = append(ends, spaceEnd)
		}
		spaceAt, spaceEnd = -1, -1
	}
	for i, r := range text {
		width := utf8.RuneLen(r)
		if width < 0 {
			width = 1
		}
		if unicode.IsSpace(r) {
			if spaceAt < 0 {
				spaceAt, spaceEnd = i, i+width
			} else {
				spaceEnd = i + width
			}
			continue
		}
		flushSpace()
		rep, ok := foldTable[r]
		if !ok {
			rep = string(r)
		}
		for k := 0; k < len(rep); k++ {
			starts = append(starts, i)
			ends = append(ends, i+width)
		}
		b.WriteString(rep)
	}
	return b.String(), starts, ends
}

// normalizeForMatch is the folded, whitespace-collapsed comparison form.
func normalizeForMatch(text string) string {
	folded, _, _ := foldText(text)
	return folded
}

// hasWord reports whether a folded needle carries at least one letter or
// digit. Punctuation-only needles would select an entry on formatting
// alone, so they never match tolerantly.
func hasWord(s string) bool {
	for _, r := range s {
		if r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

func byteMatches(entries []string, needle string) []int {
	var out []int
	for i, e := range entries {
		if strings.Contains(e, needle) {
			out = append(out, i)
		}
	}
	return out
}

// substringMatches lists the entries containing needle: byte-exact
// matches win outright, otherwise typography-folded containment.
func substringMatches(entries []string, needle string) []int {
	if hasWord(needle) {
		if out := byteMatches(entries, needle); len(out) > 0 {
			return out
		}
	}
	folded := normalizeForMatch(needle)
	if folded == "" || !hasWord(folded) {
		return nil
	}
	var out []int
	for i, e := range entries {
		if strings.Contains(normalizeForMatch(e), folded) {
			out = append(out, i)
		}
	}
	return out
}

// entryLineNumbers reports the 1-based line on which each entry starts in
// the serialized store, so an ambiguity error can point at candidates.
func entryLineNumbers(entries []string) []int {
	out := make([]int, len(entries))
	line := 1
	for i, e := range entries {
		out[i] = line
		// The entry itself, then the blank-ish "§" delimiter line.
		line += strings.Count(e, "\n") + 2
	}
	return out
}

// findEntry locates the single entry needle selects. A whole-entry match
// (exact, then folded) wins first; otherwise the substring must hit
// exactly one distinct entry. Byte-identical duplicates are safe — the
// first is used — while different entries are reported as ambiguous with
// their line numbers.
func findEntry(entries []string, needle string) (int, error) {
	needle = strings.TrimSpace(needle)
	if needle == "" {
		return -1, fmt.Errorf("old_text is required: name the entry to change")
	}
	for i, e := range entries {
		if e == needle {
			return i, nil
		}
	}
	if folded := normalizeForMatch(needle); folded != "" {
		hit := -1
		for i, e := range entries {
			if normalizeForMatch(e) != folded {
				continue
			}
			if hit >= 0 && entries[hit] != entries[i] {
				return -1, ambiguousError(entries, []int{hit, i}, needle)
			}
			if hit < 0 {
				hit = i
			}
		}
		if hit >= 0 {
			return hit, nil
		}
	}
	matches := substringMatches(entries, needle)
	if len(matches) == 0 {
		return -1, noMatchError(entries, needle)
	}
	first := entries[matches[0]]
	for _, i := range matches[1:] {
		if entries[i] != first {
			return -1, ambiguousError(entries, matches, needle)
		}
	}
	return matches[0], nil
}

func noMatchError(entries []string, needle string) error {
	msg := fmt.Sprintf("no entry contains old_text %q", clip(needle, 60))
	if close := closestEntries(entries, needle); len(close) > 0 {
		lines := entryLineNumbers(entries)
		var show []string
		for _, pair := range close {
			show = append(show, fmt.Sprintf("line %d: %q", lines[pair.index], clip(entries[pair.index], 80)))
		}
		msg += "; closest entries: " + strings.Join(show, " | ")
	}
	if strings.Contains(needle, "§") {
		msg += " (old_text must come from ONE entry — '§' separates entries; use one operation per entry)"
	}
	return fmt.Errorf("%s", msg)
}

func ambiguousError(entries []string, matches []int, needle string) error {
	lines := entryLineNumbers(entries)
	var show []string
	for _, i := range matches {
		show = append(show, fmt.Sprintf("line %d: %q", lines[i], clip(entries[i], 80)))
		if len(show) == 3 {
			break
		}
	}
	return fmt.Errorf("old_text %q matches more than one entry (%s); include more context to pick one",
		clip(needle, 60), strings.Join(show, " | "))
}

// scoredEntry pairs an entry index with its word overlap.
type scoredEntry struct {
	score float64
	index int
}

var wordRE = regexp.MustCompile(`\w+`)

// closestEntries names up to three entries sharing the most words with
// needle: a paraphrased or stale old_text usually still shares most of
// them, so the model can retry verbatim instead of guessing.
func closestEntries(entries []string, needle string) []scoredEntry {
	words := map[string]bool{}
	for _, w := range wordRE.FindAllString(strings.ToLower(needle), -1) {
		words[w] = true
	}
	if len(words) == 0 {
		return nil
	}
	var all []scoredEntry
	for i, e := range entries {
		hit := 0
		for _, w := range wordRE.FindAllString(strings.ToLower(e), -1) {
			if words[w] {
				hit++
			}
		}
		if frac := float64(hit) / float64(len(words)); frac >= 0.3 {
			all = append(all, scoredEntry{frac, i})
		}
	}
	for i := 1; i < len(all); i++ {
		for j := i; j > 0 && (all[j].score > all[j-1].score ||
			(all[j].score == all[j-1].score && all[j].index < all[j-1].index)); j-- {
			all[j], all[j-1] = all[j-1], all[j]
		}
	}
	if len(all) > 3 {
		all = all[:3]
	}
	return all
}

// spanOccurrences counts needle in text including overlapping matches:
// "00" occurs twice in "1000". An old_text that repeats inside one entry
// is not a unique address.
func spanOccurrences(text, needle string) int {
	if needle == "" || len(needle) > len(text) {
		return 0
	}
	n := 0
	for i := 0; i+len(needle) <= len(text); i++ {
		if text[i:i+len(needle)] == needle {
			n++
		}
	}
	return n
}

// locateSpan finds where needle sits inside one entry and returns the
// byte range to replace: whole entry for a whole-entry match, else the
// single exact occurrence, else the single folded occurrence mapped back
// to the original bytes. Repeated matches inside one entry are refused.
func locateSpan(entry, needle string) (int, int, error) {
	needle = strings.TrimSpace(needle)
	if needle == "" {
		return 0, 0, fmt.Errorf("old_text is required: name the text to change")
	}
	if entry == needle {
		return 0, len(entry), nil
	}
	if n := spanOccurrences(entry, needle); n == 1 {
		at := strings.Index(entry, needle)
		return at, at + len(needle), nil
	} else if n > 1 {
		return 0, 0, fmt.Errorf("old_text occurs %d times in that entry; include more surrounding text", n)
	}
	foldedNeedle := normalizeForMatch(needle)
	foldedEntry, starts, ends := foldText(entry)
	if foldedNeedle == "" || !hasWord(foldedNeedle) {
		return 0, 0, fmt.Errorf("no entry contains old_text %q", clip(needle, 60))
	}
	if foldedEntry == foldedNeedle {
		return 0, len(entry), nil
	}
	if n := spanOccurrences(foldedEntry, foldedNeedle); n == 0 {
		return 0, 0, fmt.Errorf("no entry contains old_text %q", clip(needle, 60))
	} else if n > 1 {
		return 0, 0, fmt.Errorf("old_text occurs %d times in that entry; include more surrounding text", n)
	}
	at := strings.Index(foldedEntry, foldedNeedle)
	return starts[at], ends[at+len(foldedNeedle)-1], nil
}
