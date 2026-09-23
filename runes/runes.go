// Package runes iterates UTF-8 byte strings by code point.
// Invalid bytes become single code points U+FFFD while their raw byte
// is retained for comparison, so an invalid byte only equals itself.
package runes

// R is one code point: Rune is the Unicode value (U+FFFD for an invalid
// byte), Raw is the underlying UTF-8 bytes.
type R struct {
	Rune rune
	Raw  []byte
}

// Eq reports whether two code points compare equal: raw bytes must match,
// which keeps invalid bytes distinct from one another and from U+FFFD text.
func (a R) Eq(b R) bool {
	if len(a.Raw) != len(b.Raw) {
		return false
	}
	for i := range a.Raw {
		if a.Raw[i] != b.Raw[i] {
			return false
		}
	}
	return true

}

// Slice decodes s into code points.
func Slice(s string) []R {
	rs := make([]R, 0, len(s))
	for i := 0; i < len(s); {
		r, size := decode(s[i:])
		rs = append(rs, R{Rune: r, Raw: []byte(s[i : i+size])})
		i += size
	}
	return rs
}

// Count returns the number of code points in s.
func Count(s string) int {
	n := 0
	for i := 0; i < len(s); {
		_, size := decode(s[i:])
		i += size
		n++
	}
	return n
}

func decode(b string) (rune, int) {
	c0 := b[0]
	if c0 < 0x80 {
		return rune(c0), 1
	}
	const replacement = '\uFFFD'
	if c0 < 0xC2 || len(b) < 2 || b[1]&0xC0 != 0x80 {
		return replacement, 1
	}
	c1 := b[1]
	if c0 < 0xE0 {
		return (rune(c0&0x1F) << 6) | rune(c1&0x3F), 2
	}
	if len(b) < 3 || b[2]&0xC0 != 0x80 {
		return replacement, 1
	}
	c2 := b[2]
	if c0 == 0xE0 && c1 < 0xA0 || c0 == 0xED && c1 >= 0xA0 {
		return replacement, 1
	}
	if c0 < 0xF0 {
		return (rune(c0&0x0F) << 12) | (rune(c1&0x3F) << 6) | rune(c2&0x3F), 3
	}
	if len(b) < 4 || b[3]&0xC0 != 0x80 {
		return replacement, 1
	}
	c3 := b[3]
	if c0 == 0xF0 && c1 < 0x90 || c0 == 0xF4 && c1 >= 0x90 || c0 > 0xF4 {
		return replacement, 1
	}
	return (rune(c0&0x07) << 18) | (rune(c1&0x3F) << 12) |
		(rune(c2&0x3F) << 6) | rune(c3&0x3F), 4
}
