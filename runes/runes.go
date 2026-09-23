// Package runes walks UTF-8 byte strings by code point.
//
// An invalid byte is surfaced as a single code point U+FFFD while its original
// byte is retained (R.B) and participates in literal comparisons.
package runes

import "unicode/utf8"

// R is one traversed code point together with its source bytes and offset.
type R struct {
	C rune // decoded code point (RuneError for an invalid byte)
	B byte // original leading byte; literal comparisons use this for invalid input
	N int  // source width in bytes (1..4)
	P int  // byte offset in the source string
}

// Valid reports whether the source byte was valid UTF-8.
func (r R) Valid() bool { return r.C != utf8.RuneError || r.N != 1 }

// First decodes the code point at the start of s.
func First(s string) R {
	c, n := utf8.DecodeRuneInString(s)
	var b byte
	if len(s) > 0 {
		b = s[0]
	}
	return R{C: c, B: b, N: n}
}

// At decodes the code point at byte offset p.
func At(s string, p int) R {
	r := First(s[p:])
	r.P = p
	return r
}

// Each calls f for every code point until the string ends or f returns false.
func Each(s string, f func(R) bool) {
	for p := 0; p < len(s); {
		r := At(s, p)
		if !f(r) {
			return
		}
		p += r.N
	}
}

// Slice returns all code points of s.
func Slice(s string) []R {
	var out []R
	Each(s, func(r R) bool { out = append(out, r); return true })
	return out
}

// Count returns the number of code points.
func Count(s string) int {
	n := 0
	Each(s, func(R) bool { n++; return true })
	return n
}

// Eq reports whether two code points compare equal. Two invalid bytes are equal
// only when their original bytes are equal; invalid never equals valid.
func Eq(a, b R) bool {
	if a.Valid() != b.Valid() {
		return false
	}
	if !a.Valid() {
		return a.B == b.B
	}
	return a.C == b.C
}

// Split splits s on ASCII '/' without collapsing empty segments.
func Split(s string) []string {
	if len(s) == 0 {
		return nil
	}
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}
