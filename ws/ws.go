// Package ws provides lazy classification of trailing spaces and tabs:
// a run of whitespace is only known to be line-trailing once a line ending
// or end of stream is observed.
package ws

// Pending buffers a run of spaces/tabs whose fate is not yet decided.
// It is not safe for concurrent use.
type Pending struct {
	buf []byte
}

// IsSpace reports whether b is a line-trailing whitespace byte (space or tab).
func IsSpace(b byte) bool { return b == ' ' || b == '\t' }

// Add appends a whitespace byte to the undecided run.
func (p *Pending) Add(b byte) { p.buf = append(p.buf, b) }

// Len is the number of buffered whitespace bytes.
func (p *Pending) Len() int { return len(p.buf) }

// Bytes returns the buffered run without clearing it.
func (p *Pending) Bytes() []byte { return p.buf }

// Take returns the buffered run and clears it: used when a non-whitespace
// byte proves the run was mid-line and must be emitted verbatim.
func (p *Pending) Take() []byte {
	out := p.buf
	p.buf = nil
	return out
}

// Drop discards the run: used when a line ending or end of stream proves it
// was trailing whitespace. It returns the number of dropped bytes.
func (p *Pending) Drop() int {
	n := len(p.buf)
	p.buf = nil
	return n
}
