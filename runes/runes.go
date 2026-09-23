// Package runes iterates a UTF-8 byte string by code point.
// Illegal bytes are surfaced as U+FFFD while keeping the original byte so
// equal illegal bytes in pattern and path still compare equal.
package runes

// Rune is one code point with its source byte offset and width in bytes.
type Rune struct {
	C      rune
	Offset int
	Size   int
}

const slash = '/'

// Decode returns the code points of s. Each illegal byte becomes one Rune
// with C == utf8.RuneError and Size == 1.
func Decode(s string) []Rune {
	out := make([]Rune, 0, len(s))
	for i := 0; i < len(s); {
		c, size := decodeRune(s[i:])
		out = append(out, Rune{C: c, Offset: i, Size: size})
		i += size
	}
	return out
}

// Split segments s on byte '/'; "" has zero segments, "x/" has two ("x","").
func Split(s string) [][]Rune {
	rs := Decode(s)
	var segs [][]Rune
	start := 0
	for i, r := range rs {
		if r.C == slash && r.Size == 1 {
			segs = append(segs, rs[start:i])
			start = i + 1
		}
	}
	return append(segs, rs[start:])
}

// Count is the number of code points in s.
func Count(s string) int { return len(Decode(s)) }

func decodeRune(b []byte) (rune, int) {
	if len(b) == 0 {
		return '\uFFFD', 0
	}
	x := b[0]
	switch {
	case x < 0x80:
		return rune(x), 1
	case x < 0xC2:
		return '\uFFFD', 1
	case x < 0xE0:
		if len(b) < 2 || b[1]&0xC0 != 0x80 {
			return '\uFFFD', 1
		}
		return rune(x&0x1F)<<6 | rune(b[1]&0x3F), 2
	case x < 0xF0:
		if len(b) < 3 || b[1]&0xC0 != 0x80 || b[2]&0xC0 != 0x80 {
			return '\uFFFD', 1
		}
		c := rune(x&0x0F)<<12 | rune(b[1]&0x3F)<<6 | rune(b[2]&0x3F)
		if c < 0x800 || (c >= 0xD800 && c <= 0xDFFF) {
			return '\uFFFD', 1
		}
		return c, 3
	default:
		if len(b) < 4 || b[1]&0xC0 != 0x80 || b[2]&0xC0 != 0x80 || b[3]&0xC0 != 0x80 {
			return '\uFFFD', 1
		}
		c := rune(x&0x07)<<18 | rune(b[1]&0x3F)<<12 | rune(b[2]&0x3F)<<6 | rune(b[3]&0x3F)
		if c < 0x10000 || c > 0x10FFFF {
			return '\uFFFD', 1
		}
		return c, 4
	}
}
