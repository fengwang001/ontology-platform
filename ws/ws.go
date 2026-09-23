// Package ws defers the verdict on runs of spaces and tabs: a run is trailing
// whitespace only when a line ending or end of stream proves it to be.
// It depends on no other package.
package ws

// IsSpace reports whether b is one of the two bytes this package judges:
// space (0x20) and tab (0x09).
func IsSpace(b byte) bool { return b == ' ' || b == '\t' }

// Verdict resolves a buffered run of spaces/tabs when the terminator is known.
type Verdict uint8

const (
	// Keep: the run is ordinary interior whitespace and must be emitted.
	Keep Verdict = iota
	// Drop: the run sits at end of line and must be deleted.
	Drop
)

// Decide returns Drop when the run reached a line ending, otherwise Keep.
// atEOF=true with no line ending means the run belongs to the final
// unterminated line and is still trailing whitespace, hence Drop.
func Decide(lineEnding, atEOF bool) Verdict {
	if lineEnding || atEOF {
		return Drop
	}
	return Keep
}

// Buffer accumulates an undecided whitespace run.
type Buffer struct {
	data []byte
}

// Add appends one whitespace byte.
func (b *Buffer) Add(c byte) { b.data = append(b.data, c) }

// Len reports the buffered byte count.
func (b *Buffer) Len() int { return len(b.data) }

// Bytes returns the buffered run; the slice is owned by the caller only until
// the next mutating call.
func (b *Buffer) Bytes() []byte { return b.data }

// Reset empties the buffer.
func (b *Buffer) Reset() { b.data = b.data[:0] }
