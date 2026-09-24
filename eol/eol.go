// Package eol recognizes line endings: "\r\n", lone "\r", lone "\n",
// and the pending state of a trailing "\r" at a stream split point.
package eol

// Event is the classification of an input byte relative to line endings.
type Event uint8

const (
	// Data: a byte unrelated to line endings.
	Data Event = iota
	// LF: a lone "\n" line ending (the byte did not pair with a preceding "\r").
	LF
	// CR: a lone "\r" line ending.
	CR
	// CRLF: the byte is the "\n" completing a preceding pending "\r".
	CRLF
)

// Decoder is a zero-alloc streaming classifier. Feed every byte in order;
// a trailing "\r" stays pending until the next byte or Close.
type Decoder struct {
	pendingCR bool
}

// New returns an empty Decoder.
func New() *Decoder { return &Decoder{} }

// Push feeds one byte. The bool result is true when a pending "\r" is first
// resolved as a lone line ending before ev applies to the current byte.
// A pending "\r" followed by "\n" yields (CRLF, false); followed by another
// "\r" yields (CR, true) with the new "\r" pending again; followed by an
// ordinary byte yields (Data, true).
func (d *Decoder) Push(b byte) (ev Event, priorCR bool) {
	if d.pendingCR {
		if b == '\n' {
			d.pendingCR = false
			return CRLF, false
		}
		d.pendingCR = b == '\r'
		return Data, true
	}
	if b == '\r' {
		d.pendingCR = true
	} else if b == '\n' {
		return LF, false
	}
	return Data, false
}

// Close must be called once after the final byte. It reports CR when a lone
// "\r" is still pending, otherwise Data (nothing emitted).
func (d *Decoder) Close() Event {
	if d.pendingCR {
		d.pendingCR = false
		return CR
	}
	return Data
}

// Pending reports whether a "\r" awaits the next byte.
func (d *Decoder) Pending() bool { return d.pendingCR }
