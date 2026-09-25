// Package eol recognizes line endings: "\r\n", a lone "\r", and "\n",
// including a "\r" left pending at a chunk boundary.
package eol

// Class of a byte with respect to line endings.
type Class int

const (
	Other Class = iota // any byte that is not part of a line ending
	CR                 // '\r': alone, or first half of "\r\n"
	LF                 // '\n'
)

// Kind classifies a single byte.
func Kind(b byte) Class {
	switch b {
	case '\r':
		return CR
	case '\n':
		return LF
	}
	return Other
}

// IsEOL reports whether b is '\r' or '\n'.
func IsEOL(b byte) bool { return b == '\r' || b == '\n' }

// Pend tracks a trailing '\r' whose meaning is undecided until the next
// byte (or end of stream) is seen. The zero value is no pending CR.
type Pend struct {
	On  bool // a CR is pending
	Off int  // original offset of the pending CR
}

// Set marks a CR pending at original offset off.
func (p *Pend) Set(off int) { p.On, p.Off = true, off }

// Feed resolves a pending CR against the next byte c and clears it.
// It reports lone=true when the CR stands alone (c is not '\n'); the
// caller then emits a '\n' sourced from the CR. When lone=false the CR
// is absorbed into a "\r\n" and is deleted; c itself is still processed
// normally by the caller.
func (p *Pend) Feed(c byte) (lone bool) {
	p.On = false
	return c != '\n'
}

// EOF resolves a pending CR at end of stream: it is always lone.
func (p *Pend) EOF() (lone bool) {
	p.On = false
	return true
}
