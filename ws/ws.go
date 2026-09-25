// Package ws delays classification of trailing spaces and tabs.
package ws

// IsSpace reports whether b is a removable trailing space or tab.
func IsSpace(b byte) bool { return b == ' ' || b == '\t' }

// Buffer stores a run whose fate is decided by the next byte.
type Buffer struct {
	data []byte
}

// Len is the number of buffered bytes.
func (b *Buffer) Len() int { return len(b.data) }

// Add appends one space or tab.
func (b *Buffer) Add(c byte) { b.data = append(b.data, c) }

// Bytes returns buffered content without clearing it.
func (b *Buffer) Bytes() []byte { return b.data }

// Take returns buffered content and clears the buffer.
func (b *Buffer) Take() []byte {
	out := b.data
	b.data = nil
	return out
}

// Drop clears buffered content because it was trailing whitespace.
func (b *Buffer) Drop() { b.data = nil }
