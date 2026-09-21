package ontology

import (
	"unicode"
	"unicode/utf8"
)

// decodeRunes decodes s into a rune slice. Unlike range-based decoding it
// never silently substitutes utf8.RuneError: the first invalid byte
// produces a *UTF8Error naming the side and byte offset.
func decodeRunes(s string, side byte) ([]rune, error) {
	rs := make([]rune, 0, len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			return nil, &UTF8Error{Side: side, Offset: i}
		}
		rs = append(rs, r)
		i += size
	}
	return rs, nil
}

// foldRune maps r to the smallest rune in its unicode.SimpleFold orbit.
// This is Unicode simple case folding: it is rune-to-rune and never
// changes the rune count. Full folding (e.g. 'ß' -> "ss") is out of
// scope, so "Straße" vs "STRASSE" is not equalized.
func foldRune(r rune) rune {
	lo := r
	for s := unicode.SimpleFold(r); s != r; s = unicode.SimpleFold(s) {
		if s < lo {
			lo = s
		}
	}
	return lo
}

func foldRunes(rs []rune) {
	for i := range rs {
		rs[i] = foldRune(rs[i])
	}
}

// RuneLens returns the rune (code point) counts of a and b. It returns a
// *UTF8Error if either string holds invalid UTF-8.
func RuneLens(a, b string) (la, lb int, err error) {
	ra, err := decodeRunes(a, 'a')
	if err != nil {
		return 0, 0, err
	}
	rb, err := decodeRunes(b, 'b')
	if err != nil {
		return 0, 0, err
	}
	return len(ra), len(rb), nil
}
