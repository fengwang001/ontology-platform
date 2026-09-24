// Package ws provides the delayed-decision predicate for trailing
// whitespace: a run of spaces and tabs is only known to be "trailing" once a
// line ending or the stream end is observed.
package ws

// IsWS reports whether b is a trailing-whitespace candidate (space or tab).
// Carriage return and newline are line-ending bytes, not whitespace here.
func IsWS(b byte) bool { return b == ' ' || b == '\t' }

// RunLen returns the length of the trailing space/tab run in b.
func RunLen(b []byte) int {
	n := 0
	for i := len(b) - 1; i >= 0 && IsWS(b[i]); i-- {
		n++
	}
	return n
}

// Pending buffers a run of whitespace whose fate (mid-line vs trailing) is
// not yet known. It is a plain byte slice plus the original start offset.
type Pending struct {
	Bytes []byte
	Start int
}

// Add appends one whitespace byte at original offset pos.
func (p *Pending) Add(b byte, pos int) {
	if len(p.Bytes) == 0 {
		p.Start = pos
	}
	p.Bytes = append(p.Bytes, b)
}

// Len reports the buffered byte count.
func (p *Pending) Len() int { return len(p.Bytes) }

// Drop discards the run: it was trailing whitespace.
func (p *Pending) Drop() { p.Bytes = p.Bytes[:0] }

// Take returns the run as mid-line content and clears the buffer.
func (p *Pending) Take() []byte {
	out := p.Bytes
	p.Bytes = nil
	return out
}
