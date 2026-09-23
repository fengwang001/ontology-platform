// Package runes walks UTF-8 byte strings code point by code point.
// Invalid bytes decode as U+FFFD consuming exactly one byte, so the
// original byte stays available to callers for literal comparison.
package runes

import "unicode/utf8"

// Decode returns the rune starting at byte offset i and its width.
// An invalid byte yields (utf8.RuneError, 1); i out of range yields
// (utf8.RuneError, 0).
func Decode(b []byte, i int) (rune, int) {
	if i < 0 || i >= len(b) {
		return utf8.RuneError, 0
	}
	r, size := utf8.DecodeRune(b[i:])
	if size == 0 {
		return utf8.RuneError, 1
	}
	return r, size
}

// Len counts code points in b; each invalid byte counts as one.
func Len(b []byte) int {
	n := 0
	for i := 0; i < len(b); {
		_, size := Decode(b, i)
		i += size
		n++
	}
	return n
}
