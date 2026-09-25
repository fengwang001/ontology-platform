// Package ws delays the decision on trailing spaces and tabs: a run of
// whitespace is trailing only when a line ending or stream end follows it.
// It depends on no other package in this module.
package ws

// IsSpace reports whether b is a line-trailing whitespace byte (space/tab).
func IsSpace(b byte) bool { return b == ' ' || b == '\t' }

// Buf accumulates a not-yet-decided run of spaces and tabs together with the
// original offsets of its bytes.
type Buf struct {
	data []byte
	off  []int
}

// New returns a fresh buffer with the given initial capacity.
func New(hint int) *Buf {
	if hint < 1 {
		hint = 1
	}
	return &Buf{data: make([]byte, 0, hint), off: make([]int, 0, hint)}
}

// Len is the number of buffered whitespace bytes.
func (b *Buf) Len() int { return len(b.data) }

// Add appends one whitespace byte with its original offset.
func (b *Buf) Add(c byte, off int) {
	b.data = append(b.data, c)
	b.off = append(b.off, off)
}

// Bytes returns the buffered whitespace content (nil when empty).
func (b *Buf) Bytes() []byte { return b.data }

// Offsets returns the original offsets of the buffered bytes.
func (b *Buf) Offsets() []int { return b.off }

// FirstOff returns the original offset of the first buffered byte.
func (b *Buf) FirstOff() int {
	if len(b.off) == 0 {
		return 0
	}
	return b.off[0]
}

// Reset discards the buffered run. Call it when the run is decided to be
// trailing (a line ending or stream end follows) or after flushing it out.
func (b *Buf) Reset() {
	b.data = b.data[:0]
	b.off = b.off[:0]
}
