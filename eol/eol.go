// Package eol recognizes line endings (\r\n, lone \r, \n) across chunk
// boundaries. A pending \r is kept until the following byte arrives.
package eol

// Sink receives classified events. Orig offsets are absolute byte offsets
// in the original stream.
type Sink interface {
	// Literal emits one byte that is not part of a line ending.
	Literal(b byte, orig int)
	// LineEnd emits one normalized '\n'. anchor is the original offset
	// of the representative line-ending byte: the \n for \r\n/\n, or the
	// \r for a lone \r. deleted is the \r offset in a \r\n pair, else -1.
	LineEnd(origNewline int, deletedCR int)
}

// Decoder is a streaming line-ending decoder.
type Decoder struct {
	sink Sink
	cr   int // >= 0 while a \r is awaiting the next byte
}

// NewDecoder creates a Decoder writing to sink.
func NewDecoder(sink Sink) *Decoder { return &Decoder{sink: sink, cr: -1} }

// Push feeds one byte at absolute original offset orig.
func (d *Decoder) Push(b byte, orig int) {
	if d.cr >= 0 {
		switch b {
		case '\n':
			d.sink.LineEnd(orig, d.cr)
			d.cr = -1
			return
		default:
			d.sink.LineEnd(d.cr, -1)
			d.cr = -1
		}
	}
	switch b {
	case '\r':
		d.cr = orig
	case '\n':
		d.sink.LineEnd(orig, -1)
	default:
		d.sink.Literal(b, orig)
	}
}

// Flush resolves a trailing pending \r as a lone line ending. It must be
// called at stream end (or at any known-good boundary in par).
func (d *Decoder) Flush() {
	if d.cr >= 0 {
		d.sink.LineEnd(d.cr, -1)
		d.cr = -1
	}
}

// Pending reports whether a \r awaits the next byte.
func (d *Decoder) Pending() bool { return d.cr >= 0 }
