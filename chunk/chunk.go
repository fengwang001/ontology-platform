// Package chunk splits a string into alternating maximal runs of ASCII
// digits ('0'-'9') and non-digit bytes, exposing the original text of
// each piece. Everything else, including multi-byte UTF-8 sequences,
// is treated as non-digit bytes.
package chunk

// Chunk is one maximal run of digit or non-digit bytes.
type Chunk struct {
	Text   string // original bytes of the piece
	Digits bool   // true if Text consists of ASCII digits
}

// Iter streams the chunks of a string left to right without allocating.
type Iter struct {
	s   string
	pos int
}

// New returns an iterator over the chunks of s.
func New(s string) *Iter { return &Iter{s: s} }

// Next advances to the next chunk. ok is false once the string is
// exhausted.
func (it *Iter) Next() (c Chunk, ok bool) {
	if it.pos >= len(it.s) {
		return Chunk{}, false
	}
	start := it.pos
	digits := IsDigit(it.s[it.pos])
	for it.pos < len(it.s) && IsDigit(it.s[it.pos]) == digits {
		it.pos++
	}
	return Chunk{Text: it.s[start:it.pos], Digits: digits}, true
}

// IsDigit reports whether b is an ASCII digit.
func IsDigit(b byte) bool { return '0' <= b && b <= '9' }
