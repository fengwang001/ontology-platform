// Package runes iterates a byte string by Unicode code points.
// Invalid UTF-8 bytes are each reported as a single U+FFFD code point while
// the original byte is retained for byte-exact literal comparison.
package runes

import "unicode/utf8"

// Rune is one code point. A non-zero Raw marks an invalid byte whose R is
// U+FFFD; equality then requires the same Raw byte.
type Rune struct {
	R   rune
	Raw byte
}

// Eq reports whether two code points compare equal. An invalid byte never
// equals a valid code point (even U+FFFD encoded legally).
func Eq(a, b Rune) bool { return a == b }

// Cursor walks a byte string left to right.
type Cursor struct {
	b   []byte
	pos int
}

// New returns a cursor over s.
func New(s string) *Cursor { return &Cursor{b: []byte(s)} }

// Pos is the current byte offset of the next code point.
func (c *Cursor) Pos() int { return c.pos }

// Len is the total byte length.
func (c *Cursor) Len() int { return len(c.b) }

// Done reports whether the cursor is exhausted.
func (c *Cursor) Done() bool { return c.pos >= len(c.b) }

// Next returns the current code point and advances.
func (c *Cursor) Next() (Rune, bool) {
	r, ok := c.Peek()
	if !ok {
		return Rune{}, false
	}
	if r.Raw != 0 {
		c.pos++
	} else {
		_, size := utf8.DecodeRune(c.b[c.pos:])
		c.pos += size
	}
	return r, true
}

// Peek returns the current code point without advancing.
func (c *Cursor) Peek() (Rune, bool) {
	if c.pos >= len(c.b) {
		return Rune{}, false
	}
	r, size := utf8.DecodeRune(c.b[c.pos:])
	if r == utf8.RuneError && size == 1 {
		return Rune{R: utf8.RuneError, Raw: c.b[c.pos]}, true
	}
	return Rune{R: r}, true
}

// Slice is the code points of s as a slice.
func Slice(s string) []Rune {
	c := New(s)
	var out []Rune
	for {
		r, ok := c.Next()
		if !ok {
			return out
		}
		out = append(out, r)
	}
}
