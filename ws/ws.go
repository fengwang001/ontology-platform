// Package ws buffers a run of spaces and tabs whose fate (line-trailing vs.
// in-line) is only known once a line ending or the stream end is observed.
package ws

// IsSpace reports whether b is a line-trailing whitespace byte (space/tab).
func IsSpace(b byte) bool { return b == ' ' || b == '\t' }

// Buf holds the currently undecided trailing-whitespace run.
type Buf struct {
	data []byte
}

// Len is the number of buffered whitespace bytes.
func (b *Buf) Len() int { return len(b.data) }

// Add appends one whitespace byte.
func (b *Buf) Add(c byte) { b.data = append(b.data, c) }

// Bytes returns the buffered run (valid until the next mutation).
func (b *Buf) Bytes() []byte { return b.data }

// Reset empties the buffer.
func (b *Buf) Reset() { b.data = b.data[:0] }
