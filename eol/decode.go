// Package eol identifies line endings across Write boundaries.
package eol

// Event is emitted per original input byte, in input order.
type Event int

const (
	// Byte is an ordinary byte c.
	Byte Event = iota
	// LF is a normalized line ending originating from c ('\n' or a lone '\r').
	LF
	// DropCR is the '\r' of a "\r\n" pair; immediately followed by LF.
	DropCR
)

// Decoder is a streaming line-ending splitter. A trailing '\r' stays
// pending until the next byte or Flush.
type Decoder struct {
	pending bool
}

// New returns an empty Decoder.
func New() *Decoder { return &Decoder{} }

// Feed consumes b and emits one Event per input byte.
func (d *Decoder) Feed(b []byte, emit func(ev Event, c byte)) {
	for _, c := range b {
		if d.pending {
			if c == '\n' {
				d.pending = false
				emit(DropCR, '\r')
				emit(LF, '\n')
				continue
			}
			emit(LF, '\r')
			d.pending = false
		}
		switch c {
		case '\r':
			d.pending = true
		case '\n':
			emit(LF, '\n')
		default:
			emit(Byte, c)
		}
	}
}

// Flush resolves a pending '\r' as a lone line ending. It is a no-op
// unless the last seen byte was an unmatched '\r'.
func (d *Decoder) Flush(emit func(ev Event, c byte)) {
	if d.pending {
		d.pending = false
		emit(LF, '\r')
	}
}
