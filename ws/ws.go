// Package ws defers the decision on whether a run of spaces/tabs is trailing.
package ws

// IsWS reports whether b is trailing-eligible whitespace (space or tab).
func IsWS(b byte) bool { return b == ' ' || b == '\t' }

// Buf accumulates a run of spaces/tabs whose fate is unknown until the next
// non-whitespace byte (kept) or a line ending/EOF (dropped as trailing).
type Buf struct {
	data []byte
}

// Add appends one whitespace byte.
func (b *Buf) Add(c byte) { b.data = append(b.data, c) }

// Len is the number of buffered, undecided whitespace bytes.
func (b *Buf) Len() int { return len(b.data) }

// Keep returns the buffered run because it was mid-line, and clears the buffer.
func (b *Buf) Keep() []byte {
	out := b.data
	b.data = nil
	return out
}

// Drop discards the buffered run because a line ending (or EOF) proved it
// trailing, returning the dropped bytes for bookkeeping.
func (b *Buf) Drop() []byte {
	out := b.data
	b.data = nil
	return out
}

// Bytes returns the buffered run without clearing it.
func (b *Buf) Bytes() []byte { return b.data }
