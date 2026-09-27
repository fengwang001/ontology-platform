// Package eol recognizes mixed line endings (\r\n, lone \r, \n)
// one byte at a time, including the pending state of a \r at a
// split point. It has no dependencies on other packages.
package eol

// Kind classifies an emitted event.
type Kind uint8

const (
	// Data is a regular byte (never \r or \n).
	Data Kind = iota
	// Newline means exactly one line ending was recognized.
	Newline
)

// Event is one decoder step.
type Event struct {
	Kind Kind
	Byte byte // meaningful when Kind == Data
}

// Decoder is a streaming line-ending recognizer.
//
// Feed returns a primary event and, when a previously pending
// '\r' must be flushed before the new event, a non-nil second event.
// Flush ends a stream: a dangling '\r' is a line ending.
// A Decoder is not safe for concurrent use.
type Decoder struct {
	pendingCR bool
}

// NewDecoder creates a Decoder.
func NewDecoder() *Decoder { return &Decoder{} }

// Feed ingests one byte.
func (d *Decoder) Feed(b byte) (Event, *Event) {
	if d.pendingCR {
		d.pendingCR = false
		switch b {
		case '\n':
			return Event{Kind: Newline}, nil // \r\n -> one
		case '\r':
			d.pendingCR = true
			return Event{Kind: Newline}, nil // previous lone \r; new \r pending
		default:
			d.pendingCR = false
			return Event{Kind: Newline}, &Event{Kind: Data, Byte: b}
		}
	}
	if b == '\r' {
		d.pendingCR = true
		return Event{}, nil
	}
	if b == '\n' {
		return Event{Kind: Newline}, nil
	}
	return Event{Kind: Data, Byte: b}, nil
}

// Flush reports whether a dangling '\r' is still pending at end of stream.
func (d *Decoder) Flush() bool {
	p := d.pendingCR
	d.pendingCR = false
	return p
}

// Pending reports whether a '\r' is currently held.
func (d *Decoder) Pending() bool { return d.pendingCR }
