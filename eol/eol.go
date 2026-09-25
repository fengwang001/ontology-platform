// Package eol identifies line endings across chunk boundaries.
package eol

// Event is emitted by the streaming classifier.
type Event uint8

const (
	None  Event = iota // byte b is plain content
	LF                 // b == '\n' and is a line ending
	CR                 // pending '\r' resolved as a lone line ending
	CRLF               // b == '\n' pairs with the pending '\r'
	HasCR              // b buffered as a pending '\r'
)

// Scanner is a one-byte state machine. A '\r' stays pending until the
// following byte proves whether it is CRLF or a lone CR.
type Scanner struct{ pending bool }

// New returns a clean scanner.
func New() *Scanner { return &Scanner{} }

// Feed classifies one input byte. At most one CR event is emitted (for a
// previously pending '\r'); when CR is emitted, ev still describes b.
func (s *Scanner) Feed(b byte) (ev Event, cr bool) {
	if s.pending {
		s.pending = false
		if b == '\n' {
			return CRLF, false
		}
		cr = true
	}
	switch {
	case b == '\r':
		s.pending = true
		return HasCR, cr
	case b == '\n':
		return LF, cr
	default:
		return None, cr
	}
}

// Flush resolves a pending '\r' at end of stream: true means one lone CR.
func (s *Scanner) Flush() bool {
	p := s.pending
	s.pending = false
	return p
}

// IsPending reports whether a '\r' is currently undecided.
func (s *Scanner) IsPending() bool { return s.pending }

// Reset clears all state.
func (s *Scanner) Reset() { s.pending = false }
