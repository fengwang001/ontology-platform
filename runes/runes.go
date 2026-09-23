// Package runes iterates a UTF-8 byte string by code point.
// An invalid byte is yielded as a single code point U+FFFD while its
// original byte is retained for byte-wise comparison.
package runes

// Item is one code point in the stream.
type Item struct {
	R    rune // decoded rune; RuneError for an invalid byte
	Byte byte // original first byte; equals byte(R) when valid ASCII
	Bits int  // size in bytes of the encoding (1 for an invalid byte)
	Bad  bool // true when this item is an invalid UTF-8 byte
}

// Items decodes s into code points, one Item per valid encoding or per
// invalid byte. It never panics on malformed UTF-8.
func Items(s string) []Item {
	var out []Item
	for i := 0; i < len(s); {
		r, size := decodeRuneInString(s[i:])
		if r == '\uFFFD' && size == 1 {
			out = append(out, Item{R: r, Byte: s[i], Bits: 1, Bad: true})
		} else {
			out = append(out, Item{R: r, Byte: s[i], Bits: size})
		}
		i += size
	}
	return out
}

// Eq reports whether two items compare equal. Invalid bytes compare equal
// only when their original bytes agree.
func Eq(a, b Item) bool {
	if a.Bad || b.Bad {
		return a.Bad && b.Bad && a.Byte == b.Byte
	}
	return a.R == b.R
}

// Count returns the number of code points in s.
func Count(s string) int { return len(Items(s)) }

// decodeRuneInString mirrors utf8.DecodeRuneInString without importing it
// (kept local so the package stands alone).
func decodeRuneInString(s string) (r rune, size int) {
	if len(s) == 0 {
		return '\uFFFD', 0
	}
	b0 := s[0]
	switch {
	case b0 < 0x80:
		return rune(b0), 1
	case b0 < 0xC2:
		return '\uFFFD', 1
	case b0 < 0xE0:
		if len(s) < 2 || s[1]&0xC0 != 0x80 {
			return '\uFFFD', 1
		}
		return rune(b0&0x1F)<<6 | rune(s[1]&0x3F), 2
	case b0 < 0xF0:
		if len(s) < 3 || s[1]&0xC0 != 0x80 || s[2]&0xC0 != 0x80 ||
			(b0 == 0xE0 && s[1] < 0xA0) {
			return '\uFFFD', 1
		}
		return rune(b0&0x0F)<<12 | rune(s[1]&0x3F)<<6 | rune(s[2]&0x3F), 3
	case b0 < 0xF5:
		if len(s) < 4 || s[1]&0xC0 != 0x80 || s[2]&0xC0 != 0x80 ||
			s[3]&0xC0 != 0x80 || (b0 == 0xF0 && s[1] < 0x90) ||
			(b0 == 0xF4 && s[1] >= 0x90) {
			return '\uFFFD', 1
		}
		return rune(b0&0x07)<<18 | rune(s[1]&0x3F)<<12 |
			rune(s[2]&0x3F)<<6 | rune(s[3]&0x3F), 4
	}
	return '\uFFFD', 1
}
