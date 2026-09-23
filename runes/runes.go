// Package runes walks a byte string code point by code point.
package runes

import "unicode/utf8"

// Step decodes the rune starting at s[i] and returns the rune, the raw
// bytes it occupied, and the next index. An invalid byte decodes as
// utf8.RuneError occupying exactly one byte, and raw preserves the
// original bytes so callers can compare byte-exactly.
func Step(s string, i int) (r rune, raw string, next int) {
	if i >= len(s) {
		return utf8.RuneError, "", i
	}
	if s[i] < utf8.RuneSelf {
		return rune(s[i]), s[i : i+1], i + 1
	}
	r, size := utf8.DecodeRuneInString(s[i:])
	return r, s[i : i+size], i + size
}

// Count returns the number of code points in s, counting each invalid
// byte as one code point.
func Count(s string) int {
	n := 0
	for i := 0; i < len(s); {
		_, _, i = Step(s, i)
		n++
	}
	return n
}
