// Package ws buffers a run of trailing-whitespace candidates (spaces and
// tabs) whose fate is undecided until a line ending or end of stream is
// seen. The buffer never emits and never discards by itself; the caller
// decides once the next non-whitespace byte is known.
package ws

// IsWS reports whether b is a horizontal whitespace byte (space or tab).
func IsWS(b byte) bool { return b == ' ' || b == '\t' }

// Buf holds pending whitespace bytes plus the original offset of the
// first one. lim is the maximum number of buffered bytes; 0 = unlimited.
type Buf struct {
	bs    []byte
	start int
	lim   int
}

// New returns an empty buffer with the given limit (0 = unlimited).
func New(lim int) *Buf { return &Buf{lim: lim} }

// Add appends one whitespace byte seen at original offset off. It
// reports false when the buffer is already at its limit; the byte is
// not added and the caller must turn this into a decidable error.
func (b *Buf) Add(c byte, off int) bool {
	if b.lim > 0 && len(b.bs) >= b.lim {
		return false
	}
	if len(b.bs) == 0 {
		b.start = off
	}
	b.bs = append(b.bs, c)
	return true
}

// Pending reports whether any whitespace is buffered.
func (b *Buf) Pending() bool { return len(b.bs) > 0 }

// Start is the original offset of the first buffered byte.
func (b *Buf) Start() int { return b.start }

// Len is the number of buffered bytes.
func (b *Buf) Len() int { return len(b.bs) }

// Bytes returns the buffered bytes in order.
func (b *Buf) Bytes() []byte { return b.bs }

// Reset clears the buffer; used both when the run is discarded (it was
// trailing) and after it has been flushed as content.
func (b *Buf) Reset() { b.bs = b.bs[:0] }
