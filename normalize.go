package ontology

import (
	"strings"
	"unicode"
)

// Normalize holds comparison-only key normalization toggles.
// Normalization never alters stored or returned values.
type Normalize struct {
	TrimSpace bool // trim leading/trailing Unicode whitespace
	CaseFold  bool // Unicode full case folding (not ASCII-only)
}

// apply normalizes a non-NULL value for comparison.
func (n Normalize) apply(v Value) string {
	s := v.Str
	if n.TrimSpace {
		s = strings.TrimSpace(s)
	}
	if n.CaseFold {
		s = Fold(s)
	}
	return s
}

// Fold applies Unicode full case folding: multi-rune mappings
// (e.g. ß -> ss, İ -> i+U+0307) plus simple lowercase elsewhere.
func Fold(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if rep, ok := fullFold[r]; ok {
			b.WriteString(rep)
			continue
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// fullFold lists the full (multi-rune or context-sensitive) case
// foldings that unicode.ToLower does not cover. Everything else
// folds via unicode.ToLower, which is already Unicode-aware.
var fullFold = map[rune]string{
	'ß': "ss",      // U+00DF LATIN SMALL LETTER SHARP S
	'ẞ': "ss",      // U+1E9E LATIN CAPITAL LETTER SHARP S
	'İ': "i\u0307", // U+0130 LATIN CAPITAL LETTER I WITH DOT ABOVE
	'ς': "σ",       // U+03C2 GREEK SMALL FINAL SIGMA
	'ŉ': "ʼn",      // U+0149 LATIN SMALL LETTER N PRECEDED BY APOSTROPHE
	'ﬀ': "ff",      // U+FB00 LATIN SMALL LIGATURE FF
	'ﬁ': "fi",      // U+FB01 LATIN SMALL LIGATURE FI
	'ﬂ': "fl",      // U+FB02 LATIN SMALL LIGATURE FL
	'ﬃ': "ffi",     // U+FB03 LATIN SMALL LIGATURE FFI
	'ﬄ': "ffl",     // U+FB04 LATIN SMALL LIGATURE FFL
	'ﬅ': "st",      // U+FB05 LATIN SMALL LIGATURE LONG S T
	'ﬆ': "st",      // U+FB06 LATIN SMALL LIGATURE ST
}
