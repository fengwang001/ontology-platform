// Package eol classifies streaming byte input into line endings.
// It recognizes "\r\n", a lone "\r", and "\n". A trailing pending '\r'
// stays unresolved until the next byte or Close.
package eol

// Event is the result of feeding one byte.
type Event uint8

const (
	// Other: the byte is ordinary content.
	Other Event = iota
	// LF: the byte completes a line ending ('\n' or "\r\n").
	LF
	// CR: a lone '\r' line ending; its byte is already consumed.
	CR
	// Pending: the byte '\r' may start "\r\n"; feed more or Close.
	Pending
)

// Decoder is a small state machine; zero value is ready.
type Decoder struct {
	pendingCR bool
	closed    bool
}

// Feed consumes one byte. A previously pending '\r' is resolved first:
// a following '\n' yields LF (the '\n' is swallowed by the pair), any
// other byte yields CR and the caller must still process that byte.
func (d *Decoder) Feed(b byte) Event {
	if d.pendingCR {
		d.pendingCR = false
		if b == '\n' {
			return LF
		}
		if b == '\r' {
			d.pendingCR = true
			return CR
		}
		return CR
	}
	if b == '\n' {
		return LF
	}
	if b == '\r' {
		d.pendingCR = true
		return Pending
	}
	return Other
}

// Pending reports whether a '\r' awaits resolution.
func (d *Decoder) Pending() bool { return d.pendingCR }

// Close resolves any trailing '\r' as a lone line ending.
func (d *Decoder) Close() Event {
	if d.pendingCR {
		d.pendingCR = false
		d.closed = true
		return CR
	}
	d.closed = true
	return Other
}

// Reset returns the decoder to its zero state.
func (d *Decoder) Reset() { *d = Decoder{} }
