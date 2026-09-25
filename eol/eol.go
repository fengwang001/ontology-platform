// Package eol classifies streaming line endings.
package eol

// Decoder resolves CR, LF, and CRLF across Write boundaries.
type Decoder struct {
	pendingCR bool
}

// NewDecoder returns an empty decoder.
func NewDecoder() *Decoder { return &Decoder{} }

// Pending reports whether a trailing CR waits for the next byte.
func (d *Decoder) Pending() bool { return d.pendingCR }

// End emits a trailing lone CR at stream end and clears pending state.
func (d *Decoder) End() bool {
	ended := d.pendingCR
	d.pendingCR = false
	return ended
}

// Feed consumes one byte. cr means a completed CR or CRLF; lf means bare LF.
func (d *Decoder) Feed(b byte) (cr, lf bool) {
	if d.pendingCR {
		d.pendingCR = false
		if b == '\n' {
			return true, false
		}
		cr = true
		if b == '\r' {
			d.pendingCR = true
		} else if b == '\n' {
			lf = true
		}
		return cr, lf
	}
	if b == '\r' {
		d.pendingCR = true
	}
	return false, b == '\n'
}
