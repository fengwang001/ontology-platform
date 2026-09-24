// Package eol recognizes line endings in a byte stream: CRLF, lone CR, LF,
// plus the undecided state of a trailing CR sitting at a chunk boundary.
package eol

// Kind classifies a decoded event.
type Kind uint8

const (
	// Text is any byte that is not part of a line ending.
	Text Kind = iota
	// LF is a line ending produced by a lone '\n'.
	LF
	// CR is a line ending produced by a lone '\r'.
	CR
	// CRLF is the two-byte line ending "\r\n".
	CRLF
)

// Event is one decoded unit. Pos is the original byte offset of the unit:
// for Text the byte itself; for CRLF the position of '\r'; for CR/LF the
// position of the line-ending byte.
type Event struct {
	Kind Kind
	Pos  int
}

// Decoder is a streaming line-ending recognizer. A pending CR is held until
// the next byte decides whether it joins an LF into CRLF.
type Decoder struct {
	pendingCR bool
	pos       int // offset of the pending CR
	next      int // offset to assign to the next pushed byte
}

// New returns a zeroed decoder.
func New() *Decoder { return &Decoder{} }

// Reset clears all state, including the byte stream position.
func (d *Decoder) Reset() { *d = Decoder{} }

// Next reports the offset the next pushed byte will have.
func (d *Decoder) Next() int { return d.next }

// Pending reports whether a CR is currently undecided.
func (d *Decoder) Pending() bool { return d.pendingCR }

// Push feeds one byte and returns zero, one or two events.
func (d *Decoder) Push(b byte) []Event {
	p := d.next
	d.next++
	if d.pendingCR {
		cp := d.pos
		d.pendingCR = false
		if b == '\n' {
			return []Event{{Kind: CRLF, Pos: cp}}
		}
		if b == '\r' {
			d.pendingCR = true
			d.pos = p
			return []Event{{Kind: CR, Pos: cp}}
		}
		return []Event{{Kind: CR, Pos: cp}, {Kind: Text, Pos: p}}
	}
	switch b {
	case '\r':
		d.pendingCR = true
		d.pos = p
		return nil
	case '\n':
		return []Event{{Kind: LF, Pos: p}}
	default:
		return []Event{{Kind: Text, Pos: p}}
	}
}

// Flush resolves a trailing CR as a lone line ending at stream end.
func (d *Decoder) Flush() []Event {
	if d.pendingCR {
		d.pendingCR = false
		return []Event{{Kind: CR, Pos: d.pos}}
	}
	return nil
}
