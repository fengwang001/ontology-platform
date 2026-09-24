// Package ws delays the decision about a run of spaces and tabs: it is
// trailing only when a line ending or stream end is eventually seen.
package ws

// IsSpace reports whether b is a line-trailing whitespace byte,
// i.e. an ASCII space or tab.
func IsSpace(b byte) bool { return b == ' ' || b == '\t' }

// Buf buffers a currently-undecided run of spaces and tabs. It is not
// safe for concurrent use.
type Buf struct {
	data  []byte
	limit int
}

// NewBuf creates a buffer; limit <= 0 means unlimited.
func NewBuf(limit int) *Buf {
	return &Buf{data: make([]byte, 0, 64), limit: limit}
}

// Add appends one space/tab byte. It returns false when the configured
// limit would be exceeded; the byte is not stored in that case.
func (b *Buf) Add(c byte) bool {
	if b.limit > 0 && len(b.data) >= b.limit {
		return false
	}
	b.data = append(b.data, c)
	return true
}

// Len returns the number of buffered bytes.
func (b *Buf) Len() int { return len(b.data) }

// Bytes returns the buffered content; valid until the next mutation.
func (b *Buf) Bytes() []byte { return b.data }

// Drop discards the whole run (it was line trailing).
func (b *Buf) Drop() { b.data = b.data[:0] }

// Flush returns the buffered content as ordinary text (it was followed by
// another ordinary byte) and empties the buffer.
func (b *Buf) Flush() []byte {
	out := b.data
	b.data = make([]byte, 0, cap(out))
	return out
}

// Reset restores the buffer to its initial state.
func (b *Buf) Reset() { b.data = b.data[:0] }
