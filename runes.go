package ontology

import (
	"unicode"
	"unicode/utf8"
)

// decodeRunes decodes s into a rune slice. Invalid UTF-8 produces a
// *UTF8Error carrying the side and the byte offset of the first bad byte;
// nothing is silently replaced with utf8.RuneError.
func decodeRunes(s string, side Side) ([]rune, error) {
	runes := make([]rune, 0, len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			return nil, &UTF8Error{Side: side, ByteOffset: i}
		}
		runes = append(runes, r)
		i += size
	}
	return runes, nil
}

// RuneLen returns the number of Unicode code points in s. If s contains
// invalid UTF-8, it returns a *UTF8Error (with Side == SideNone) whose
// ByteOffset locates the first offending byte.
func RuneLen(s string) (int, error) {
	n := 0
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			return 0, &UTF8Error{Side: SideNone, ByteOffset: i}
		}
		n++
		i += size
	}
	return n, nil
}

// foldRune maps r to the smallest rune in its unicode.SimpleFold orbit,
// giving a canonical representative for simple (1:1) case folding.
func foldRune(r rune) rune {
	min := r
	for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
		if f < min {
			min = f
		}
	}
	return min
}

// foldRunes applies foldRune to every rune in place.
func foldRunes(rs []rune) {
	for i, r := range rs {
		rs[i] = foldRune(r)
	}
}
