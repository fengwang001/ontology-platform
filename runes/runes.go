// Package runes traverses UTF-8 byte strings by code point.
//
// An invalid byte is surfaced as a single code point U+FFFD while retaining
// the original byte. Bad code points compare unequal to valid code points and
// to each other unless they carry the same original byte.
package runes

import "unicode/utf8"

// Rune is one code point. Bad is non-zero only for an invalid source byte.
type Rune struct {
	R   rune
	Bad byte
}

// IsBad reports whether r came from an invalid UTF-8 byte.
func (r Rune) IsBad() bool { return r.Bad != 0 }

// Equal reports exact equality, including retained invalid bytes.
func (r Rune) Equal(o Rune) bool { return r == o }

// Cmp returns -1/0/1 by code point; an invalid byte sorts by its raw value.
func (r Rune) Cmp(o Rune) int {
	a, b := r.R, o.R
	if r.IsBad() {
		a = int32(r.Bad)
	}
	if o.IsBad() {
		b = int32(o.Bad)
	}
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// All decodes s into code points. Each illegal byte becomes one Rune{RuneError}.
func All(s string) []Rune {
	out := make([]Rune, 0, len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			out = append(out, Rune{R: utf8.RuneError, Bad: s[i]})
		} else {
			out = append(out, Rune{R: r})
		}
		i += size
	}
	return out
}

// First decodes and returns the leading code point and its byte width.
func First(s string) (Rune, int) {
	r, size := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError && size == 1 {
		return Rune{R: utf8.RuneError, Bad: s[0]}, 1
	}
	return Rune{R: r}, size
}
