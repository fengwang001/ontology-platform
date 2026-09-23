package runes

import "unicode/utf8"

// Point is one logical code point. Invalid UTF-8 is one byte represented by
// RuneError while Byte keeps the original byte for byte-exact comparison.
type Point struct {
	Rune rune
	Byte byte
	Size int
}

// Decode returns the logical code point at offset.
func Decode(s string, offset int) (Point, int) {
	if offset >= len(s) {
		return Point{}, 0
	}
	r, size := utf8.DecodeRuneInString(s[offset:])
	p := Point{Rune: r, Size: size}
	if r == utf8.RuneError && size == 1 {
		p.Byte = s[offset]
	}
	return p, size
}

// Range calls f for every logical code point. Iteration stops if f returns
// false and reports whether all points were visited.
func Range(s string, f func(offset int, p Point) bool) bool {
	for offset := 0; offset < len(s); {
		p, size := Decode(s, offset)
		if !f(offset, p) {
			return false
		}
		offset += size
	}
	return true
}

// Count returns the number of logical code points.
func Count(s string) int {
	n := 0
	Range(s, func(int, Point) bool {
		n++
		return true
	})
	return n
}

// Equal reports whether two code points are equal. Invalid bytes compare by
// their original byte, while valid runes compare by rune.
func Equal(a, b Point) bool {
	if a.Rune != b.Rune {
		return false
	}
	if a.Rune == utf8.RuneError && a.Size == 1 {
		return a.Byte == b.Byte
	}
	return true
}
