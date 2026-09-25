// Package eol recognizes line endings in a byte stream, including the
// one-byte-pending state when '\r' is seen at a cut point.
package eol

// Event is the result of feeding one byte.
type Event uint8

const (
	EventPending Event = iota // '\r' seen, following byte unknown
	EventLF                   // '\n' (or the '\n' half of a CRLF)
	EventCRLF                 // two-byte CRLF line ending
	EventData                 // ordinary content byte
)

// Decoder is a tiny state machine: call Feed per byte; when the previous byte
// was '\r', Feed reports whether the new byte completes a CRLF.
type Decoder struct{ pendingCR bool }

// New returns a Decoder.
func New() *Decoder { return &Decoder{} }

// Feed reports the event for b given prior state; pending is true afterward
// when b is a '\r' whose successor has not yet been seen.
func (d *Decoder) Feed(b byte) (ev Event, pending bool) {
	if d.pendingCR {
		d.pendingCR = false
		if b == '\n' {
			return EventCRLF, false
		}
		if b == '\r' {
			d.pendingCR = true
			return EventLF, true
		}
		return EventData, false
	}
	if b == '\r' {
		d.pendingCR = true
		return EventPending, true
	}
	if b == '\n' {
		return EventLF, false
	}
	return EventData, false
}

// Pending reports whether the last fed byte was a dangling '\r'.
func (d *Decoder) Pending() bool { return d.pendingCR }

// Flush is called at stream end; it reports a lone pending '\r' as one LF.
func (d *Decoder) Flush() Event {
	if d.pendingCR {
		d.pendingCR = false
		return EventLF
	}
	return EventData
}
