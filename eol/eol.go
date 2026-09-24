// Package eol recognizes mixed line endings (\r\n, lone \r, lone \n)
// across arbitrary write boundaries. A trailing \r stays pending until
// the next byte (or Close) resolves it.
package eol

// Decoder is a small state machine holding at most one pending CR.
// It is not safe for concurrent use.
type Decoder struct {
	pendingCR bool
}

// NewDecoder returns a ready decoder.
func NewDecoder() *Decoder { return &Decoder{} }

// Feed resolves one input byte.
//
//	emitLF:          a normalized '\n' must be appended before b is handled
//	consumeNormally: b must then be processed as an ordinary input byte
//
// When consumeNormally is false the byte is fully consumed by the line
// ending (held as CR or swallowed as the LF part of CRLF).
func (d *Decoder) Feed(b byte) (emitLF, consumeNormally bool) {
	if !d.pendingCR {
		if b == '\r' {
			d.pendingCR = true
			return false, false
		}
		return false, true
	}
	switch b {
	case '\n': // CRLF: one line ending for two bytes
		d.pendingCR = false
		return true, false
	case '\r': // previous lone CR ends a line; new CR becomes pending
		return true, false
	default: // previous lone CR ends a line; b is ordinary
		d.pendingCR = false
		return true, true
	}
}

// Close reports whether a still-pending CR is a lone line ending.
func (d *Decoder) Close() (emitLF bool) {
	if d.pendingCR {
		d.pendingCR = false
		return true
	}
	return false
}

// Pending reports whether a CR is currently held unresolved.
func (d *Decoder) Pending() bool { return d.pendingCR }

// Reset restores the decoder to its initial state.
func (d *Decoder) Reset() { d.pendingCR = false }
