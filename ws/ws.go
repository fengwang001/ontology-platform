// Package ws implements delayed classification of trailing horizontal
// whitespace. A run of spaces and tabs is only known to be trailing once a
// line ending or end of stream arrives, so it must be buffered until then.
package ws

// IsTrailing reports whether b is trailing-whitespace candidate: a space or a
// tab. NUL, \r, \n and other bytes are never buffered here.
func IsTrailing(b byte) bool { return b == ' ' || b == '\t' }

// Buffer holds one contiguous run of not-yet-classified spaces/tabs. The run
// is contiguous in the original stream, so original offsets are
// Start, Start+1, ... for each buffered byte.
type Buffer struct {
	Start int // original offset of Bytes[0]; undefined when empty
	Bytes []byte
}

// NewBuffer returns an empty pending-whitespace buffer.
func NewBuffer() *Buffer { return &Buffer{} }

// Len is the number of buffered bytes.
func (p *Buffer) Len() int { return len(p.Bytes) }

// Empty reports whether nothing is buffered.
func (p *Buffer) Empty() bool { return len(p.Bytes) == 0 }

// Add appends b at original offset off. off must equal Start+Len() for the
// first and every subsequent byte of the run.
func (p *Buffer) Add(off int, b byte) {
	if len(p.Bytes) == 0 {
		p.Start = off
	}
	p.Bytes = append(p.Bytes, b)
}

// Take removes and returns the buffered bytes (preserving their order) so the
// caller can emit them when the run turns out not to be trailing.
func (p *Buffer) Take() []byte {
	out := p.Bytes
	p.Bytes = nil
	return out
}

// Drop discards the buffered bytes: the run was trailing whitespace.
func (p *Buffer) Drop() { p.Bytes = nil }

// Reset clears the buffer.
func (p *Buffer) Reset() { p.Bytes = nil }
