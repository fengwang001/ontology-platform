// Package eol identifies line endings in a byte stream whose chunks may be
// split at any point, including between '\r' and '\n'.
package eol

// Kind is the kind of line ending that becomes known at the current byte.
type Kind int

const (
	// None means no line ending completed at this byte.
	None Kind = iota
	// LF is a lone '\n'.
	LF
	// CR is a lone '\r' (not followed by '\n').
	CR
	// CRLF is the two-byte sequence "\r\n".
	CRLF
)

// Scanner is a streaming, split-point-insensitive line ending scanner.
// It remembers a trailing '\r' until the next byte decides whether it is
// part of CRLF or a lone CR.
type Scanner struct {
	pendingCR bool
}

// Reset returns the scanner to its initial state.
func (s *Scanner) Reset() { s.pendingCR = false }

// Pending reports whether a trailing '\r' is awaiting resolution.
func (s *Scanner) Pending() bool { return s.pendingCR }

// Feed consumes one byte. The returned kind is non-None only when a line
// ending becomes fully known:
//
//   - CRLF is returned on the '\n' byte that resolves a pending '\r'.
//   - LF is returned on a '\n' with no pending '\r'.
//   - CR is returned for a pending '\r' when the next byte proves it was
//     lone (another '\r' or any byte other than '\n'). In that case the
//     current byte has already been accounted for by the scanner (it is
//     either a fresh pending '\r' or ordinary content).
func (s *Scanner) Feed(b byte) Kind {
	if !s.pendingCR {
		if b == '\r' {
			s.pendingCR = true
		} else if b == '\n' {
			return LF
		}
		return None
	}
	switch b {
	case '\n':
		s.pendingCR = false
		return CRLF
	case '\r':
		// Previous '\r' was lone; current '\r' becomes the new pending one.
		return CR
	default:
		s.pendingCR = false
		return CR
	}
}

// Flush is called at stream end. It resolves a pending '\r' as a lone CR.
func (s *Scanner) Flush() Kind {
	if s.pendingCR {
		s.pendingCR = false
		return CR
	}
	return None
}
