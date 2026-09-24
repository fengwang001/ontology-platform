// Package ws delays the classification of trailing spaces and tabs.
// A run of spaces/tabs is buffered until a newline (it is trailing and gets
// dropped) or a non-space byte / end of stream (it is inline and is kept).
package ws

// IsSpace reports whether b is a trailing-whitespace candidate.
func IsSpace(b byte) bool { return b == ' ' || b == '\t' }

// Buf is a bounded pending-whitespace run.
type Buf struct {
	data []byte
}

// Add appends a space byte. The caller must first check Overflow.
func (b *Buf) Add(c byte) { b.data = append(b.data, c) }

// Pending reports whether the buffer holds bytes.
func (b *Buf) Pending() bool { return len(b.data) > 0 }

// Len is the buffered byte count.
func (b *Buf) Len() int { return len(b.data) }

// Bytes returns the buffered bytes (valid until the next mutation).
func (b *Buf) Bytes() []byte { return b.data }

// Overflow reports whether adding one byte would exceed limit bytes.
// A zero limit disables the bound.
func (b *Buf) Overflow(limit int) bool {
	return limit > 0 && len(b.data) >= limit
}

// Take removes and returns the buffered bytes (inline case).
func (b *Buf) Take() []byte {
	out := b.data
	b.data = nil
	return out
}

// Drop discards the buffered bytes (trailing case).
func (b *Buf) Drop() { b.data = nil }

// Reset clears the buffer.
func (b *Buf) Reset() { b.data = nil }
