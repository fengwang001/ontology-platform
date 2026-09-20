// Package partscan implements a delimiter-driven segmented scanner.
//
// A stream starts with "--<boundary>\r\n", parts are separated by
// "\r\n--<boundary>\r\n", and it ends with "\r\n--<boundary>--". Bytes are
// fed incrementally in arbitrarily sized chunks; Feed returns the parts that
// became complete with each call. Part contents are never parsed or decoded.
package partscan

// Scanner incrementally extracts parts from a boundary-delimited byte stream.
// A Scanner is not safe for concurrent use.
type Scanner struct {
	boundary string

	// delimiter is the part-internal boundary marker, always
	// "\r\n--<boundary>".
	delimiter []byte
	// preamble is the required stream opening: "--<boundary>\r\n".
	preamble []byte

	match *kmp // matcher for delimiter while reading parts

	phase phase
	err   error // sticky terminal error

	// buf holds bytes for the current part (phasePart), or the not-yet
	// fully matched stream prefix (phasePreamble). It never retains bytes
	// of already finished parts.
	buf []byte

	// pos indexes buf and tracks how far the matcher has advanced. Bytes
	// before pos are committed part content (phasePart only); when a
	// match is disproven by a look-ahead byte, pos advances by one and
	// the matcher continues from its fallback state.
	pos int
}

type phase uint8

const (
	phasePreamble phase = iota // waiting to verify the opening marker
	phasePart                  // accumulating a part, scanning for delimiter
	phaseDone                  // closing delimiter seen
	phaseError                 // terminal error
)

// New returns a Scanner expecting the given MIME-style boundary marker.
func New(boundary string) *Scanner {
	delim := make([]byte, 0, 4+len(boundary))
	delim = append(delim, '\r', '\n', '-', '-')
	delim = append(delim, boundary...)

	pre := make([]byte, 0, 4+len(boundary))
	pre = append(pre, '-', '-')
	pre = append(pre, boundary...)
	pre = append(pre, '\r', '\n')

	return &Scanner{
		boundary:  boundary,
		delimiter: delim,
		preamble:  pre,
		match:     newKMP(delim),
	}
}

// Done reports whether the closing delimiter has been seen.
func (s *Scanner) Done() bool { return s.phase == phaseDone }

// Pending returns the number of buffered bytes not yet committed to a
// finished part: the in-progress part plus any unmatched look-ahead tail.
func (s *Scanner) Pending() int { return len(s.buf) }

// Close marks the stream finished. It returns ErrIncomplete unless the
// closing delimiter was seen. Repeated calls return the same result.
func (s *Scanner) Close() error {
	switch s.phase {
	case phaseDone:
		return nil
	case phaseError:
		return s.err
	default:
		return ErrIncomplete
	}
}
