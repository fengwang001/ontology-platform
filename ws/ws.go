// Package ws delays the decision about a run of spaces and tabs: only when a
// line ending or end of stream arrives can the run be known to be trailing.
package ws

// Is reports whether b is part of a trailing-whitespace candidate.
func Is(b byte) bool { return b == ' ' || b == '\t' }

// Buffer accumulates a single undecided run of spaces and tabs.
type Buffer struct {
	run []byte
}

// Add appends one whitespace byte to the pending run.
func (b *Buffer) Add(c byte) { b.run = append(b.run, c) }

// Len is the number of buffered whitespace bytes.
func (b *Buffer) Len() int { return len(b.run) }

// Run returns the buffered bytes (valid until Keep or Reset).
func (b *Buffer) Run() []byte { return b.run }

// Keep reports that a non-whitespace byte followed the run, so the run is
// mid-line: it returns the bytes that must be emitted before that byte and
// clears the buffer.
func (b *Buffer) Keep() []byte {
	out := b.run
	b.run = nil
	return out
}

// Trim reports that a line ending or EOF followed the run, so it is trailing
// whitespace: the bytes are discarded and the buffer cleared.
func (b *Buffer) Trim() { b.run = nil }

// Reset clears any pending run.
func (b *Buffer) Reset() { b.run = b.run[:0] }

// Take removes and returns the pending run (ownership transfers to caller).
func (b *Buffer) Take() []byte {
	out := b.run
	b.run = nil
	return out
}
