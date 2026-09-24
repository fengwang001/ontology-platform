// Package eol identifies mixed line endings across stream boundaries.
package eol

const (
	// None means the current byte is not a line ending.
	None byte = 0
	// LF means a lone "\n".
	LF byte = '\n'
	// CR means a lone "\r".
	CR byte = '\r'
	// CRLF means a two-byte "\r\n".
	CRLF byte = 'B'
)

// Scanner remembers whether the previous byte was an unmatched CR.
type Scanner struct {
	pendingCR bool
}

// Reset returns the scanner to its initial state.
func (s *Scanner) Reset() { s.pendingCR = false }

// PendingCR reports whether the scanner ends with an unmatched CR.
func (s *Scanner) PendingCR() bool { return s.pendingCR }

// Feed consumes one byte and reports the completed line ending, if any.
// CRLF is emitted when LF resolves a pending CR; the prior CR emitted nothing.
func (s *Scanner) Feed(b byte) byte {
	if s.pendingCR {
		s.pendingCR = false
		if b == '\n' {
			return CRLF
		}
	}
	if b == '\r' {
		s.pendingCR = true
		return None
	}
	return b
}

// Close emits a pending lone CR as a line ending at stream end.
func (s *Scanner) Close() byte {
	if s.pendingCR {
		s.pendingCR = false
		return CR
	}
	return None
}
