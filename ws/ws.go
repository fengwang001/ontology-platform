// Package ws performs delayed classification of trailing spaces and tabs.
package ws

// Space reports whether b is trailing-whitespace candidate (space or tab).
func Space(b byte) bool { return b == ' ' || b == '\t' }

// Buffer holds a run of spaces/tabs until a line ending or EOF proves it
// trailing. It never holds non-whitespace bytes.
type Buffer struct {
	data []byte
}

// Add appends one whitespace byte.
func (b *Buffer) Add(c byte) { b.data = append(b.data, c) }

// Len is the number of currently held whitespace bytes.
func (b *Buffer) Len() int { return len(b.data) }

// Pending returns the held bytes without clearing them.
func (b *Buffer) Pending() []byte { return b.data }

// Flush returns the held bytes and clears the run (whitespace was not
// trailing: either a non-whitespace byte follows, or Keep at EOF).
func (b *Buffer) Flush() []byte {
	out := b.data
	b.data = nil
	return out
}

// Drop discards the held run (a line ending or EOF proves it trailing).
func (b *Buffer) Drop() { b.data = nil }
