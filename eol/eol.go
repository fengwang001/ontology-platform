// Package eol recognizes the three line-ending shapes across stream
// split points: CRLF, a lone CR, and LF.
//
// A CR is a pending boundary until the next byte is known: CR followed by
// LF is one CRLF ending; otherwise the CR is a lone ending. The pending
// state is what makes recognition identical for any chunking of the input.
package eol

// Boundary classifies a resolved line ending.
type Boundary uint8

const (
	// None means no line ending was resolved.
	None Boundary = iota
	// LoneLF is a \n that was not part of a CRLF pair.
	LoneLF
	// LoneCR is a \r not followed by \n.
	LoneCR
	// CRLF is the two-byte sequence \r\n.
	CRLF

	CR byte = '\r'
	LF byte = '\n'
)

// IsCR reports whether b is a carriage return.
func IsCR(b byte) bool { return b == CR }

// IsLF reports whether b is a line feed.
func IsLF(b byte) bool { return b == LF }

// Decoder is a single-byte-at-a-time line-ending recognizer.
//
// Usage: call Feed for every input byte in order; when Feed reports
// consumed==false the caller must feed the same byte again (the byte
// resolved a previously pending CR but is itself unprocessed). At stream
// end call Flush to resolve any CR still pending at a split point.
type Decoder struct {
	pending bool
}

// Pending reports whether a CR is awaiting its following byte.
func (d *Decoder) Pending() bool { return d.pending }

// Reset returns the decoder to its initial state.
func (d *Decoder) Reset() { d.pending = false }

// Feed processes one byte. It returns the boundary resolved at this step
// (None when nothing is resolved) and whether b was consumed. When b
// resolves a pending CR but is not the LF of a CRLF pair, consumed is
// false and the byte must be fed again.
func (d *Decoder) Feed(b byte) (boundary Boundary, consumed bool) {
	if !d.pending {
		switch {
		case b == CR:
			d.pending = true
			return None, true
		case b == LF:
			return LoneLF, true
		default:
			return None, true
		}
	}

	// A CR is pending: this byte decides its fate.
	d.pending = false
	switch {
	case b == LF:
		return CRLF, true
	case b == CR:
		// Previous CR was a lone ending; this CR starts a new pending one.
		d.pending = true
		return LoneCR, true
	default:
		// Previous CR was a lone ending; b itself is still unprocessed.
		return LoneCR, false
	}
}

// Flush resolves any CR pending at stream end as a lone ending.
func (d *Decoder) Flush() Boundary {
	if !d.pending {
		return None
	}
	d.pending = false
	return LoneCR
}
