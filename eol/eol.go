// Package eol recognizes \r\n, lone \r and \n line endings, and keeps a lone
// trailing \r pending until the next byte or stream end resolves it.
package eol

// Kind classifies a fed byte relative to line endings.
type Kind uint8

const (
	Data Kind = iota // ordinary byte
	CR               // lone \r ending (the \r is consumed)
	LF               // \n or the \n of a \r\n pair (the \r is consumed)
	Pend             // \r buffered, resolution deferred
)

// Decoder is a streaming line-ending decoder. Zero value is ready.
type Decoder struct{ cr bool }

// Feed feeds one byte. off is informational and copied into the event.
// A single input byte yields at most one event: a \r\n pair first emits Pend
// for the \r; the following \n yields LF (and consumes the pending \r).
func (d *Decoder) Feed(b byte, off int) (kind Kind, n int) {
	if d.cr {
		d.cr = false
		if b == '\n' {
			return LF, 2
		}
		return CR, 1
	}
	switch b {
	case '\n':
		return LF, 1
	case '\r':
		d.cr = true
		return Pend, 1
	default:
		return Data, 1
	}
}

// Flush resolves a pending \r at a boundary. When ending is true (stream end)
// the \r counts as a lone line ending; when false (segment cut) it stays data.
func (d *Decoder) Flush(ending bool) (kind Kind, yes bool) {
	if !d.cr {
		return Data, false
	}
	d.cr = false
	if ending {
		return CR, true
	}
	return Data, true
}

// Pending reports whether a \r is buffered.
func (d *Decoder) Pending() bool { return d.cr }
