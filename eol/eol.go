// Package eol recognizes mixed line endings: CRLF, lone CR and LF.
// A trailing CR is kept pending until the next byte decides CRLF vs lone CR.
package eol

// Event kind.
const (
	Lit  byte = 0 // ordinary byte (including NUL and invalid UTF-8 bytes)
	CR   byte = 1 // lone CR line ending, originating at Pos
	LF   byte = 2 // lone LF line ending, originating at Pos
	CRLF byte = 3 // CRLF line ending, originating at Pos
)

// Event is one decoded input byte or one recognized line ending.
type Event struct {
	Kind byte
	Byte byte // valid when Kind == Lit
	Pos  int  // original input offset of the originating byte
}

// Decoder is a streaming line-ending recognizer.
type Decoder struct {
	pendingCR bool
	pos       int // total bytes consumed
}

// Reset restores the decoder to its initial state.
func (d *Decoder) Reset() { *d = Decoder{} }

// Feed consumes one byte. e1 is always an event; e2 is a second event
// (a trailing line ending flushed before the new byte) when two is true.
func (d *Decoder) Feed(b byte) (e1, e2 Event, two bool) {
	pos := d.pos
	d.pos++
	if d.pendingCR {
		d.pendingCR = false
		switch b {
		case '\n':
			return Event{Kind: CRLF, Pos: pos - 1}, Event{}, false
		case '\r':
			d.pendingCR = true
			return Event{Kind: CR, Pos: pos - 1}, Event{}, false
		default:
			e2 = Event{Kind: CR, Pos: pos - 1}
			return Event{Kind: Lit, Byte: b, Pos: pos}, e2, true
		}
	}
	if b == '\r' {
		d.pendingCR = true
		return Event{}, Event{}, false
	}
	if b == '\n' {
		return Event{Kind: LF, Pos: pos}, Event{}, false
	}
	return Event{Kind: Lit, Byte: b, Pos: pos}, Event{}, false
}

// Flush resolves a trailing CR as a lone line ending at stream end.
func (d *Decoder) Flush() (Event, bool) {
	if d.pendingCR {
		d.pendingCR = false
		return Event{Kind: CR, Pos: d.pos - 1}, true
	}
	return Event{}, false
}

// Pending reports whether a CR is awaiting decision.
func (d *Decoder) Pending() bool { return d.pendingCR }
