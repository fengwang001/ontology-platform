// Package eol recognizes the three line-ending forms (\r\n, lone \r, \n)
// across arbitrary byte-chunk boundaries. A trailing \r stays pending until
// the next byte or End resolves it.
package eol

// Event is the result of feeding one byte.
type Event int

const (
	// None means no line ending was resolved by this byte.
	None Event = iota
	// Newline means a line ending was resolved; At is the original byte
	// offset of its representative byte (\n for CRLF/LF, the \r for CR).
	Newline
)

// Decoder is a single-use streaming line-ending decoder.
type Decoder struct {
	cr  bool // a \r is waiting to see whether the next byte is \n
	pos int  // number of bytes fed
}

// Push feeds one byte and returns the resolved event. A flushed pending \r
// (current byte is not \n) reports Newline anchored at the previous byte and
// the current byte still remains to be processed by the caller.
func (d *Decoder) Push(b byte) (Event, int) {
	at := d.pos
	d.pos++
	switch {
	case b == '\n':
		d.cr = false
		return Newline, at
	case b == '\r':
		if d.cr {
			d.cr = true
			return Newline, at - 1
		}
		d.cr = true
		return None, at
	case d.cr:
		d.cr = false
		return Newline, at - 1
	default:
		return None, at
	}
}

// Pending reports whether a \r is currently undecided.
func (d *Decoder) Pending() bool { return d.cr }

// End must be called once after the last byte. It flushes a pending lone \r.
func (d *Decoder) End() (Event, int) {
	if d.cr {
		d.cr = false
		return Newline, d.pos - 1
	}
	return None, d.pos
}
