// Package eol identifies line endings: CRLF, lone CR, and LF.
// A trailing CR is "pending" until the next byte decides CRLF vs lone CR.
package eol

// Class categorizes a byte for the normalization state machine.
type Class uint8

const (
	Other Class = iota // any byte that is passed through unchanged
	LF
	CR
	Space
	NUL
)

// Classify a single byte.
func Classify(b byte) Class {
	switch {
	case b == '\n':
		return LF
	case b == '\r':
		return CR
	case b == ' ' || b == '\t':
		return Space
	case b == 0:
	return NUL
	default:
		return Other
	}
}

// Decoder tracks one pending CR across write boundaries.
type Decoder struct {
	pending bool
}

// Pending reports whether a CR is waiting for the next byte.
func (d *Decoder) Pending() bool { return d.pending }

// Reset clears the pending state.
func (d *Decoder) Reset() { d.pending = false }

// Feed reports what a pending CR (if any) becomes when the next byte arrives.
// Returned flags: crlf means CR+LF consumed together; lone means a CR that is
// itself a line ending. After a call with a CR, Pending becomes true.
func (d *Decoder) Feed(c Class) (crlf, lone bool) {
	if d.pending {
		if c == LF {
			d.pending = false
			return true, false
		}
		d.pending = false
		lone = true
	}
	if c == CR {
		d.pending = true
	}
	return false, lone
}

// Flush resolves a pending CR at end of stream as a lone line ending.
func (d *Decoder) Flush() bool {
	if d.pending {
		d.pending = false
		return true
	}
	return false
}
