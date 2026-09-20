package ontology

import (
	"strings"
	"unicode"
)

// NormOptions controls how keys are normalized before comparison.
// Each rule can be toggled independently. Normalization never
// changes stored or returned values; it only affects conflict
// detection.
type NormOptions struct {
	// TrimSpace strips leading and trailing Unicode whitespace.
	TrimSpace bool
	// CaseFold enables Unicode full case folding (not a plain
	// ASCII lowercasing): "ß" matches "SS", "İ" matches "i̇",
	// and word-final "ς" matches "σ" and "Σ".
	CaseFold bool
}

// Normalize applies the enabled rules to s and returns the
// comparison key. Rules compose: with only CaseFold enabled,
// "  Alice" and "alice" still differ because the whitespace is
// kept; both must be enabled for them to collide.
func (n NormOptions) Normalize(s string) string {
	if n.TrimSpace {
		s = strings.TrimSpace(s)
	}
	if n.CaseFold {
		s = foldString(s)
	}
	return s
}

// foldString applies Unicode full case folding to s.
func foldString(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if folded, ok := fullFold(r); ok {
			for _, fr := range folded {
				b.WriteRune(simpleFoldMin(fr))
			}
			continue
		}
		b.WriteRune(simpleFoldMin(r))
	}
	return b.String()
}

// simpleFoldMin returns the canonical representative of r's simple
// case-folding equivalence class (its SimpleFold cycle): the
// smallest cycle member that is already lowercase, so "A" and "a"
// share the key "a", and "Σ", "σ" and "ς" all fold to one key.
func simpleFoldMin(r rune) rune {
	min := r
	best := r
	hasLower := unicode.ToLower(r) == r
	for s := unicode.SimpleFold(r); s != r; s = unicode.SimpleFold(s) {
		if s < min {
			min = s
		}
		if unicode.ToLower(s) == s && (!hasLower || s < best) {
			best, hasLower = s, true
		}
	}
	if hasLower {
		return best
	}
	return min
}
