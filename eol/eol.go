// Package eol recognizes \r\n, lone \r and \n line endings across Write cuts.
package eol

// Kind is the classification of an emitted event.
type Kind uint8

const (
	Data   Kind = iota // a non-line-ending byte
	Line               // a normalized line ending, one event per line
	Pending            // a \r seen at a cut boundary; may merge with next \n
)

// Event is one classification result.
// For Line, b is the consumed final byte ('\r' for a lone \r, '\n' for \r\n or \n)
// and n is the number of input bytes consumed (1 or 2).
type Event struct {
	Kind Kind
	B    byte
	N    int
}

// Decoder is a streaming line-ending recognizer.
//
// A \r is emitted as Pending immediately; the following input byte resolves it
// (Feed prepends a resolution event). Close resolves a dangling \r as a lone
// line ending. Not safe for concurrent use.
type Decoder struct {
	pending bool
}

// Feed classifies one input byte. ev0 carries the resolution of a previously
// pending \r when ok0 is true; ev1 carries the classification of b.
func (d *Decoder) Feed(b byte) (ev0 Event, ok0 bool, ev1 Event) {
	if d.pending {
		d.pending = false
		switch b {
		case '\n':
			ev0, ok0 = Event{Kind: Line, B: '\n', N: 2}, true
			return // b fully consumed by the \r\n pair
		case '\r':
			ev0, ok0 = Event{Kind: Line, B: '\r', N: 1}, true
			d.pending = true
			return // \r starts a new pending pair
		default:
			ev0, ok0 = Event{Kind: Line, B: '\r', N: 1}, true
		}
	}
	switch b {
	case '\r':
		d.pending = true
		ev1 = Event{Kind: Pending, B: '\r', N: 1}
	case '\n':
		ev1 = Event{Kind: Line, B: '\n', N: 1}
	default:
		ev1 = Event{Kind: Data, B: b, N: 1}
	}
	return
}

// Flush resolves any dangling \r as a lone line ending. It returns at most one
// event (Line, N=1) or no event.
func (d *Decoder) Flush() (Event, bool) {
	if d.pending {
		d.pending = false
		return Event{Kind: Line, B: '\r', N: 1}, true
	}
	return Event{}, false
}

// Pending reports whether a \r is awaiting the next byte.
func (d *Decoder) Pending() bool { return d.pending }

// Reset restores the decoder to its initial state.
func (d *Decoder) Reset() { d.pending = false }
